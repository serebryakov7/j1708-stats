package main

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"time"

	"github.com/tarm/serial"
	bolt "go.etcd.io/bbolt"

	"github.com/serebryakov7/j1708-stats/common"
	"github.com/serebryakov7/j1708-stats/pkg/mqtt" // Added for StartProcessingDTCs
	"github.com/serebryakov7/j1708-stats/pkg/storage"
)

const (
	interFrameGap = 4 * time.Millisecond
)

// Bus реализует интерфейс Bus для протокола J1587
type Bus struct {
	port      *serial.Port
	data      *J1587Data // Теперь это ссылка на структуру из data.go
	frames    chan []byte
	stopChan  chan struct{}
	isRunning bool
	dtcChan   chan common.DTCCode // Канал для отправки DTC
	db        *bolt.DB            // База данных для дедупликации DTC
}

// NewBus создает новый экземпляр J1587Protocol
func NewBus(port *serial.Port) (*Bus, error) {
	db, err := storage.OpenDB("agent_j1587_dtc.db") // Используем уникальное имя БД
	if err != nil {
		return nil, fmt.Errorf("ошибка открытия БД для DTC: %w", err)
	}
	log.Println("База данных DTC agent_j1587_dtc.db успешно открыта.")

	return &Bus{
		port:     port,
		data:     NewJ1587Data(), // Инициализируем пустую структуру J1587Data
		frames:   make(chan []byte),
		stopChan: make(chan struct{}),
		dtcChan:  make(chan common.DTCCode, 10), // Буферизированный канал для DTC
		db:       db,
	}, nil
}

// Close закрывает ресурсы Bus, включая базу данных.
func (p *Bus) Close() error {
	log.Println("Закрытие ресурсов Bus...")
	if p.db != nil {
		log.Println("Закрытие БД DTC...")
		if err := p.db.Close(); err != nil {
			log.Printf("Ошибка при закрытии БД DTC: %v", err)
			// Продолжаем закрывать другие ресурсы, если они есть
		}
	}
	// Здесь можно добавить закрытие других ресурсов, если потребуется
	return nil
}

// StartReading начинает чтение данных с порта
func (p *Bus) StartReading() error {
	if p.isRunning {
		return fmt.Errorf("протокол J1587 уже запущен")
	}
	if p.port == nil {
		return fmt.Errorf("порт не был инициализирован")
	}

	p.isRunning = true
	go p.readFrames()
	go p.processFrames()

	return nil
}

// StopReading останавливает чтение данных
func (p *Bus) StopReading() error {
	if !p.isRunning {
		return nil
	}

	close(p.stopChan)
	p.isRunning = false
	return nil
}

// GetData возвращает актуальные данные транспортного средства
func (p *Bus) GetData() json.Marshaler {
	return p.data // J1587Data реализует VehicleData через методы с мьютексами
}

// SendFrame отправляет J1587 фрейм в последовательный порт
func (p *Bus) SendFrame(mid byte, pid byte, data []byte) error {
	if p.port == nil {
		return fmt.Errorf("порт не инициализирован для отправки команды")
	}
	if !p.isRunning {
		return fmt.Errorf("протокол J1587 не запущен, отправка команды невозможна")
	}

	// Формируем фрейм: MID, PID, Data
	var frame []byte
	frame = append(frame, mid)
	frame = append(frame, pid)
	if data != nil {
		frame = append(frame, data...)
	}

	// Рассчитываем и добавляем контрольную сумму согласно SAE J1587
	checksum := calculateJ1587Checksum(frame)
	frameWithChecksum := append(frame, checksum)

	log.Printf("J1587 SENDING FRAME: MID=%d PID=%d DATA=% X CHECKSUM=%d", mid, pid, data, checksum)
	_, err := p.port.Write(frameWithChecksum)
	if err != nil {
		return fmt.Errorf("ошибка отправки J1587 команды: %v", err)
	}

	time.Sleep(50 * time.Millisecond) // Можно настроить

	return nil
}

// ClearActiveDTCs отправляет команду для сброса активных DTC
// targetMID - идентификатор модуля, которому адресована команда (например, MID двигателя)
func (p *Bus) ClearActiveDTCs(targetMID byte) error {
	var commandData []byte

	err := p.SendFrame(targetMID, PID_COMMAND_CLEAR_DTCS, commandData)
	if err != nil {
		return fmt.Errorf("не удалось отправить команду сброса DTC J1587: %v", err)
	}
	log.Printf("Команда сброса DTC J1587 отправлена на MID: %d", targetMID)

	// Очищаем хранилище дедупликации DTC
	if p.db != nil {
		log.Println("Очистка хранилища дедупликации DTC...")
		if err := storage.ClearAll(p.db); err != nil {
			// Логируем ошибку, но не прерываем основной процесс,
			// так как команда на ECU уже могла уйти.
			log.Printf("Ошибка очистки хранилища DTC: %v", err)
		} else {
			log.Println("Хранилище дедупликации DTC успешно очищено.")
		}
	}
	return nil
}

// StartProcessingDTCs запускает обработку и дедупликацию DTC.
func (p *Bus) StartProcessingDTCs(mqttClient *mqtt.MQTTClient) {
	log.Println("Запуск обработки DTC для J1587 с использованием хранилища...")
	for {
		select {
		case <-p.stopChan:
			log.Println("Остановка обработки DTC (канал stopChan закрыт).")
			return
		case dtc, ok := <-p.dtcChan:
			if !ok {
				log.Println("Канал DTC закрыт, завершение обработки DTC.")
				return
			}
			log.Printf("Получен DTC J1587: %+v (SPN: %d, FMI: %d)", dtc, dtc.SPN, dtc.FMI)

			isNew, err := storage.IsNew(p.db, uint32(dtc.SPN), uint8(dtc.FMI))
			if err != nil {
				log.Printf("Ошибка проверки DTC (SPN: %d, FMI: %d) в хранилище: %v", dtc.SPN, dtc.FMI, err)
				continue
			}

			if isNew {
				log.Printf("Новый DTC J1587 (SPN: %d, FMI: %d), отправка в MQTT.", dtc.SPN, dtc.FMI)
				mqttClient.PublishDTC(dtc)
			} else {
				log.Printf("Дубликат DTC J1587 (SPN: %d, FMI: %d) пропущен.", dtc.SPN, dtc.FMI)
			}
		}
	}
}

// readFrames читает фреймы из последовательного порта
func (p *Bus) readFrames() {
	buf := make([]byte, 128)
	var frame []byte
	last := time.Now()

	for {
		select {
		case <-p.stopChan:
			return
		default:
			n, err := p.port.Read(buf)
			now := time.Now()

			if err != nil && err != io.EOF {
				log.Printf("Ошибка чтения порта: %v", err)
			}

			if n == 0 {
				// таймаут чтения
				if len(frame) > 0 && now.Sub(last) >= interFrameGap {
					p.frames <- frame
					frame = nil
				}
				continue
			}

			for i := 0; i < n; i++ {
				if now.Sub(last) >= interFrameGap && len(frame) > 0 {
					p.frames <- frame
					frame = nil
				}
				frame = append(frame, buf[i])
				last = now
			}
		}
	}
}
