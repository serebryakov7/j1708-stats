//go:build linux

package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"time" // Добавлен импорт time

	bolt "go.etcd.io/bbolt"
	"golang.org/x/sys/unix"

	"github.com/serebryakov7/j1708-stats/common"
)

// J1939FrameInfo содержит информацию о кадре J1939.
type J1939FrameInfo struct {
	PGN  uint32
	SA   uint8
	Data []byte
}

// Bus реализует логику для протокола J1939
type Bus struct {
	fd               int // Сырой файловый дескриптор для сокета J1939
	data             *J1939Data
	framesCh         chan J1939FrameInfo
	stopChan         chan struct{}
	dtcChan          chan common.DTCCode
	canInterfaceName string
	frameProcessor   *FrameProcessor
	localSA          uint8
	ifaceIndex       int // Добавлено для SendCommand
}

// NewBus создает новый экземпляр Bus.
// Инициализирует J1939 SOCK_DGRAM сокет и привязывает его.
// Принимает *bolt.DB для передачи в FrameProcessor.
func NewBus(canInterface string, db *bolt.DB) (*Bus, error) { // Добавлен параметр db
	fd, err := unix.Socket(unix.AF_CAN, unix.SOCK_DGRAM, unix.CAN_J1939)
	if err != nil {
		return nil, fmt.Errorf("не удалось создать сокет J1939: %w", err)
	}

	iface, err := net.InterfaceByName(canInterface)
	if err != nil {
		unix.Close(fd)
		return nil, fmt.Errorf("InterfaceByName %q: %w", canInterface, err)
	}

	// J1939_NO_ADDR (обычно 0) используется для динамического назначения адреса ядром
	// J1939_NO_NAME (0) и J1939_NO_PGN (0) для wildcard привязки
	sa := &unix.SockaddrCANJ1939{
		Ifindex: iface.Index,
		Name:    0, // J1939_NO_NAME
		PGN:     0, // J1939_NO_PGN (wildcard PGN for reception)
		Addr:    0, // Заменяем unix.J1939_NO_ADDR на 0 для динамического назначения адреса
	}

	if err := unix.Bind(fd, sa); err != nil {
		unix.Close(fd)
		return nil, fmt.Errorf("не удалось привязать сокет J1939: %w", err)
	}

	// Получаем назначенный адрес источника (SA)
	localSockAddr, err := unix.Getsockname(fd)
	if err != nil {
		unix.Close(fd)
		return nil, fmt.Errorf("не удалось получить имя сокета J1939: %w", err)
	}

	j1939LocalAddr, ok := localSockAddr.(*unix.SockaddrCANJ1939)
	if !ok {
		unix.Close(fd)
		return nil, fmt.Errorf("неожиданный тип адреса сокета после привязки: %T", localSockAddr)
	}
	log.Printf("Сокет J1939 привязан, назначенный SA: 0x%02X (%d) на интерфейсе %s (ifindex %d)", j1939LocalAddr.Addr, j1939LocalAddr.Addr, canInterface, iface.Index)

	p := &Bus{
		fd:               fd,
		data:             NewJ1939Data(),
		framesCh:         make(chan J1939FrameInfo, 100), // Буферизированный канал для кадров
		dtcChan:          make(chan common.DTCCode, 10),  // Буферизированный канал для DTC
		stopChan:         make(chan struct{}),
		canInterfaceName: canInterface,
		localSA:          j1939LocalAddr.Addr,
		ifaceIndex:       iface.Index, // Сохраняем индекс интерфейса
	}
	// Передаем db в NewFrameProcessor
	p.frameProcessor = NewFrameProcessor(p.data, p.dtcChan, db) // Изменено: передаем db
	return p, nil
}

// Start запускает горутины для чтения и обработки кадров.
func (p *Bus) Start() {
	log.Println("Запуск протокола J1939...")
	go p.readFrames()
	go p.processFrames()
	log.Println("Протокол J1939 запущен.")
}

// Stop останавливает обработку J1939 и закрывает ресурсы.
func (p *Bus) Stop() error {
	log.Println("Остановка протокола J1939...")

	if p.stopChan != nil {
		select {
		case <-p.stopChan:
			log.Println("Stop: stopChan уже был закрыт.")
		default:
			close(p.stopChan)
			log.Println("Stop: stopChan закрыт.")
		}
	} else {
		log.Println("Предупреждение: Stop() вызван, когда stopChan уже nil.")
	}

	if p.fd != -1 { // Используем -1 как индикатор закрытого/неинициализированного fd
		log.Printf("Закрытие J1939 сокета (fd %d)...", p.fd)
		err := unix.Close(p.fd)
		if err != nil {
			log.Printf("Ошибка при закрытии J1939 сокета (fd %d): %v", p.fd, err)
		} else {
			log.Printf("J1939 сокет (fd %d) успешно закрыт.", p.fd)
		}
		p.fd = -1 // Помечаем fd как закрытый
	} else {
		log.Println("J1939 сокет уже был закрыт (fd == -1) или не был инициализирован.")
	}

	log.Println("Протокол J1939 остановлен.")
	return nil
}

// GetData возвращает текущие данные J1939.
func (p *Bus) GetData() json.Marshaler {
	return p.data.Copy() // Используем метод Copy() для безопасного доступа
}

// GetDTCChannel возвращает канал для получения DTC.
func (p *Bus) GetDTCChannel() <-chan common.DTCCode {
	return p.dtcChan
}

// processFrames обрабатывает кадры из framesCh.
func (p *Bus) processFrames() {
	log.Println("Горутина обработки кадров J1939 запущена.")
	defer func() {
		log.Println("Горутина обработки кадров J1939 остановлена.")
		close(p.dtcChan) // Закрываем dtcChan, когда обработка кадров завершена
	}()

	for {
		select {
		case frame, ok := <-p.framesCh:
			if !ok {
				log.Println("Канал кадров J1939 закрыт, выход из горутины обработки.")
				return
			}
			// log.Printf("Обработка кадра: PGN=0x%X, SA=0x%X, DataLen=%d", frame.PGN, frame.SA, len(frame.Data))
			p.frameProcessor.ProcessFrame(frame.PGN, frame.SA, frame.Data)
		case <-p.stopChan:
			log.Println("Получен сигнал остановки в горутине обработки кадров J1939.")
			return
		}
	}
}

// SendCommand отправляет команду J1939.
func (p *Bus) SendCommand(pgn uint32, data []byte, destAddr uint8) error {
	if p.fd == -1 {
		return fmt.Errorf("невозможно отправить команду: сокет J1939 закрыт")
	}
	if len(data) > 8 { // J1939 фреймы данных ограничены 8 байтами без TP
		return fmt.Errorf("длина данных превышает 8 байт (%d), TP не реализован", len(data))
	}

	// Адрес назначения для SockaddrCANJ1939
	destSockAddr := &unix.SockaddrCANJ1939{
		Ifindex: p.ifaceIndex, // Используем сохраненный индекс интерфейса
		Name:    0,            // J1939_NO_NAME
		PGN:     pgn,          // PGN для отправки
		Addr:    destAddr,     // Адрес назначения
	}

	log.Printf("Отправка J1939 команды: PGN=0x%X (%d), SA=0x%X, DA=0x%X, IfaceIdx=%d, Data=%X", pgn, pgn, p.localSA, destAddr, p.ifaceIndex, data)

	// Флаги для Sendto обычно 0 для J1939
	err := unix.Sendto(p.fd, data, 0, destSockAddr)
	if err != nil {
		return fmt.Errorf("ошибка отправки J1939 команды через unix.Sendto: %w", err)
	}

	log.Printf("Команда PGN 0x%X для DA 0x%X отправлена. Ожидание ACK не реализовано.", pgn, destAddr)
	return nil
}

// readFrames читает кадры из сокета J1939.
func (p *Bus) readFrames() {
	log.Println("Горутина чтения кадров J1939 запущена.")
	buffer := make([]byte, 2048) // Буфер для чтения данных кадра J1939 (макс. размер TP пакета ~1785 байт)
	defer func() {
		log.Println("Горутина чтения кадров J1939 остановлена.")
		close(p.framesCh) // Закрываем framesCh, когда чтение завершено
	}()

	for {
		select {
		case <-p.stopChan:
			log.Println("Получен сигнал остановки в горутине чтения кадров J1939.")
			return
		default:
			// Установка таймаута для операции чтения, чтобы не блокироваться навечно
			// и периодически проверять stopChan.
			// Это можно сделать с помощью unix.Setsockopt с SO_RCVTIMEO,
			// или используя select с тайм-аутом, если бы Recvfrom был неблокирующим.
			// Поскольку Recvfrom блокирующий, лучший способ - закрыть сокет из Stop().

			if p.fd == -1 { // Проверка, если сокет уже закрыт
				log.Println("Сокет J1939 закрыт, выход из горутины чтения.")
				return
			}

			n, from, err := unix.Recvfrom(p.fd, buffer, 0)
			if err != nil {
				select {
				case <-p.stopChan: // Если stopChan закрыт, это ожидаемое завершение
					log.Println("Recvfrom завершился из-за закрытия stopChan (вероятно, сокет был закрыт).")
					return
				default:
					// Если ошибка не связана с закрытием сокета (например, syscall.EINTR), можно продолжить
					// или обработать ее соответствующим образом.
					// Ошибка syscall.EBADF (Bad file descriptor) означает, что сокет был закрыт.
					if errors.Is(err, unix.EBADF) || errors.Is(err, net.ErrClosed) {
						log.Println("Recvfrom: сокет был закрыт, выход из горутины чтения.")
						return
					}
					log.Printf("Ошибка чтения из сокета J1939: %v. Продолжение работы...", err)
					// Можно добавить небольшую задержку перед повторной попыткой, чтобы избежать слишком частого логирования ошибок
					time.Sleep(100 * time.Millisecond)
					continue
				}
			}

			if n == 0 { // Нет данных, или отправитель закрыл соединение (не типично для DGRAM)
				continue
			}

			sockAddr, ok := from.(*unix.SockaddrCANJ1939)
			if !ok {
				log.Printf("Получен кадр от неизвестного типа адреса: %T", from)
				continue
			}

			// Копируем данные, так как buffer будет перезаписан
			frameData := make([]byte, n)
			copy(frameData, buffer[:n])

			frameInfo := J1939FrameInfo{
				PGN:  sockAddr.PGN,
				SA:   sockAddr.Addr, // Адрес источника
				Data: frameData,
			}

			// Отправляем в канал для обработки, но не блокируемся, если канал полон
			select {
			case p.framesCh <- frameInfo:
				// Успешно отправлено
			case <-p.stopChan:
				log.Println("Получен сигнал остановки при попытке отправить кадр в framesCh.")
				return
			default:
				log.Printf("Канал framesCh полон. Кадр PGN 0x%X от SA 0x%X пропущен.", frameInfo.PGN, frameInfo.SA)
			}
		}
	}
}
