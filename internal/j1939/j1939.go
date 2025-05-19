package j1939

import (
	"encoding/json"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/serebryakov7/j1708-stats/internal/protocol"
	"github.com/tarm/serial"
)

const (
	interFrameGap = 5 * time.Millisecond
)

// J1939 PGN (Parameter Group Numbers)
const (
	PGN_ELECTRONIC_ENGINE_CONTROLLER_1       = 0x00F004 // Electronic Engine Controller 1
	PGN_ELECTRONIC_TRANSMISSION_CONTROLLER_2 = 0x00F005 // Electronic Transmission Controller 2
	PGN_VEHICLE_POSITION                     = 0x00FE4E // Vehicle Position
	PGN_FUEL_CONSUMPTION                     = 0x00FEE9 // Fuel Consumption
	PGN_AMBIENT_CONDITIONS                   = 0x00FEF5 // Ambient Conditions
	PGN_DM1                                  = 0x00FECA // DM1 (Active Diagnostic Trouble Codes)
	PGN_DM2                                  = 0x00FEBF // DM2 (Previously Active Diagnostic Trouble Codes)
)

// J1939Data содержит все данные автомобиля, собираемые с шины J1939
type J1939Data struct {
	Timestamp         time.Time          `json:"timestamp"`
	Speed             *float64           `json:"speed,omitempty"`
	EngineRPM         *float64           `json:"engine_rpm,omitempty"`
	EngineCoolantTemp *float64           `json:"coolant_temp,omitempty"`
	EngineOilPressure *float64           `json:"oil_pressure,omitempty"`
	EngineLoad        *float64           `json:"engine_load,omitempty"`
	FuelLevel         *float64           `json:"fuel_level,omitempty"`
	FuelConsumption   *float64           `json:"fuel_consumption,omitempty"`
	BatteryVoltage    *float64           `json:"battery_voltage,omitempty"`
	AmbientAirTemp    *float64           `json:"ambient_temp,omitempty"`
	TotalDistance     *float64           `json:"total_distance,omitempty"`
	Latitude          *float64           `json:"latitude,omitempty"`
	Longitude         *float64           `json:"longitude,omitempty"`
	Altitude          *float64           `json:"altitude,omitempty"`
	ActiveDTCCodes    []protocol.DTCCode `json:"active_dtc_codes,omitempty"`
	PreviousDTCCodes  []protocol.DTCCode `json:"previous_dtc_codes,omitempty"`
	RawFrames         map[uint32][]byte  `json:"-"` // Хранит последние необработанные фреймы по PGN
	mutex             sync.Mutex         `json:"-"`
}

// Реализация методов интерфейса VehicleData
func (d *J1939Data) GetTimestamp() time.Time {
	return d.Timestamp
}

func (d *J1939Data) SetTimestamp(timestamp time.Time) {
	d.Timestamp = timestamp
}

func (d *J1939Data) ToJSON() ([]byte, error) {
	return json.Marshal(d)
}

// J1939Protocol реализует интерфейс Protocol для протокола J1939
type J1939Protocol struct {
	port      *serial.Port
	data      *J1939Data
	frames    chan []byte
	stopChan  chan struct{}
	isRunning bool
}

// Описание источников сообщений
var sourceDescriptions = map[uint8]string{
	0:   "Двигатель",
	3:   "Трансмиссия",
	33:  "Система управления тормозами",
	136: "Приборная панель",
	140: "Электронный блок управления кузовом",
	// Можно добавить больше согласно спецификации
}

// Описания кодов неисправности J1939 SPN-FMI
var fmiDescriptions = map[int]string{
	0:  "Данные выше нормального рабочего диапазона",
	1:  "Данные ниже нормального рабочего диапазона",
	2:  "Некорректные, перемежающиеся или неверные данные",
	3:  "Напряжение выше нормы или короткое замыкание на высокое напряжение",
	4:  "Напряжение ниже нормы или короткое замыкание на низкое напряжение",
	5:  "Низкий ток или обрыв цепи",
	6:  "Высокий ток или короткое замыкание на массу",
	7:  "Механическая система не отвечает или не откалибрована",
	8:  "Ненормальная частота, период или ширина импульса",
	9:  "Ненормальная скорость обновления",
	10: "Ненормальная скорость изменения",
	11: "Неопределенная неисправность",
	12: "Неисправное устройство или компонент",
	13: "Некорректная калибровка",
	14: "Особые инструкции",
	15: "Данные выше нормального рабочего диапазона (наименьшая серьезность)",
	16: "Данные выше нормального рабочего диапазона (средняя серьезность)",
	17: "Данные ниже нормального рабочего диапазона (наименьшая серьезность)",
	18: "Данные ниже нормального рабочего диапазона (средняя серьезность)",
	19: "Получены ошибочные данные от сети",
	20: "Данные отклоняются вверх (наименьшая серьезность)",
	21: "Данные отклоняются вниз (наименьшая серьезность)",
	31: "Условие существует",
}

// NewJ1939 создает новый экземпляр J1939Protocol
func NewJ1939() protocol.Protocol {
	protocol.NewJ1939Protocol = func() protocol.Protocol {
		return &J1939Protocol{
			data: &J1939Data{
				RawFrames: make(map[uint32][]byte),
			},
			frames:   make(chan []byte),
			stopChan: make(chan struct{}),
		}
	}
	return protocol.NewJ1939Protocol()
}

// Initialize инициализирует протокол
func (p *J1939Protocol) Initialize(port *serial.Port) error {
	p.port = port
	return nil
}

// StartReading начинает чтение данных с порта
func (p *J1939Protocol) StartReading() error {
	if p.isRunning {
		return fmt.Errorf("протокол J1939 уже запущен")
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
func (p *J1939Protocol) StopReading() error {
	if !p.isRunning {
		return nil
	}

	close(p.stopChan)
	p.isRunning = false
	return nil
}

// GetData возвращает актуальные данные транспортного средства
func (p *J1939Protocol) GetData() protocol.VehicleData {
	p.data.mutex.Lock()
	defer p.data.mutex.Unlock()
	return p.data
}

// GetProtocolType возвращает тип протокола
func (p *J1939Protocol) GetProtocolType() protocol.ProtocolType {
	return protocol.J1939Protocol
}

// readFrames читает фреймы из последовательного порта
func (p *J1939Protocol) readFrames() {
	buf := make([]byte, 256) // J1939 может иметь большие пакеты
	var frame []byte
	last := time.Now()

	for {
		select {
		case <-p.stopChan:
			return
		default:
			n, err := p.port.Read(buf)
			now := time.Now()

			if err != nil && err.Error() != "EOF" {
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

// processFrames обрабатывает полученные фреймы
func (p *J1939Protocol) processFrames() {
	for {
		select {
		case <-p.stopChan:
			return
		case frame := <-p.frames:
			if len(frame) < 10 { // Минимальный размер фрейма J1939
				continue
			}

			// J1939 использует 29-битный идентификатор (extended CAN)
			// Парсим идентификатор (первые 4 байта в большинстве реализаций)
			canID := uint32(frame[0])<<24 | uint32(frame[1])<<16 | uint32(frame[2])<<8 | uint32(frame[3])

			// Извлекаем PGN (Parameter Group Number)
			pgn := (canID >> 8) & 0x3FFFF

			// Источник сообщения (Source Address)
			sa := uint8(canID & 0xFF)

			// Данные сообщения
			data := frame[4:]

			// Выводим фрейм для отладки
			fmt.Printf("J1939 FRAME: PGN=0x%X SA=%d DATA=% X\n", pgn, sa, data)

			// Обрабатываем сообщение
			p.parseFrame(pgn, sa, data)
		}
	}
}

// parseFrame разбирает фрейм J1939
func (p *J1939Protocol) parseFrame(pgn uint32, sa uint8, data []byte) {
	p.data.mutex.Lock()
	defer p.data.mutex.Unlock()

	// Сохраняем сырые данные для каждого PGN
	p.data.RawFrames[pgn] = make([]byte, len(data))
	copy(p.data.RawFrames[pgn], data)

	// Парсинг различных параметров по их PGN
	switch pgn {
	case PGN_ELECTRONIC_ENGINE_CONTROLLER_1:
		p.parseEngineController1(data)
	case PGN_ELECTRONIC_TRANSMISSION_CONTROLLER_2:
		p.parseTransmissionController2(data)
	case PGN_VEHICLE_POSITION:
		p.parseVehiclePosition(data)
	case PGN_FUEL_CONSUMPTION:
		p.parseFuelConsumption(data)
	case PGN_AMBIENT_CONDITIONS:
		p.parseAmbientConditions(data)
	case PGN_DM1:
		p.parseDM1(data, sa)
	case PGN_DM2:
		p.parseDM2(data, sa)
	}
}

// parseEngineController1 парсит данные от электронного блока управления двигателем
func (p *J1939Protocol) parseEngineController1(data []byte) {
	if len(data) < 8 {
		return
	}

	// Обороты двигателя (байты 3-4)
	if data[3] != 0xFF || data[4] != 0xFF {
		rpmRaw := uint16(data[3]) | uint16(data[4])<<8
		rpm := float64(rpmRaw) * 0.125
		p.data.EngineRPM = &rpm
	}

	// Нагрузка двигателя (байт 2)
	if data[2] != 0xFF {
		load := float64(data[2]) * 0.4
		p.data.EngineLoad = &load
	}
}

// parseTransmissionController2 парсит данные от электронного блока управления трансмиссией
func (p *J1939Protocol) parseTransmissionController2(data []byte) {
	if len(data) < 8 {
		return
	}

	// Скорость (байты 1-2)
	if data[1] != 0xFF || data[2] != 0xFF {
		speedRaw := uint16(data[1]) | uint16(data[2])<<8
		speed := float64(speedRaw) * 0.00390625
		p.data.Speed = &speed
	}
}

// parseVehiclePosition парсит данные о позиции транспортного средства
func (p *J1939Protocol) parseVehiclePosition(data []byte) {
	if len(data) < 8 {
		return
	}

	// Широта (байты 0-3)
	latRaw := uint32(data[0]) | uint32(data[1])<<8 | uint32(data[2])<<16 | uint32(data[3])<<24
	if latRaw != 0xFFFFFFFF {
		lat := float64(latRaw) * 0.0000001
		p.data.Latitude = &lat
	}

	// Долгота (байты 4-7)
	lonRaw := uint32(data[4]) | uint32(data[5])<<8 | uint32(data[6])<<16 | uint32(data[7])<<24
	if lonRaw != 0xFFFFFFFF {
		lon := float64(lonRaw) * 0.0000001
		p.data.Longitude = &lon
	}
}

// parseFuelConsumption парсит данные о потреблении топлива
func (p *J1939Protocol) parseFuelConsumption(data []byte) {
	if len(data) < 8 {
		return
	}

	// Потребление топлива (байты 0-3)
	if data[0] != 0xFF || data[1] != 0xFF || data[2] != 0xFF || data[3] != 0xFF {
		fuelRaw := uint32(data[0]) | uint32(data[1])<<8 | uint32(data[2])<<16 | uint32(data[3])<<24
		consumption := float64(fuelRaw) * 0.001 // л/ч
		p.data.FuelConsumption = &consumption
	}
}

// parseAmbientConditions парсит данные об окружающих условиях
func (p *J1939Protocol) parseAmbientConditions(data []byte) {
	if len(data) < 8 {
		return
	}

	// Температура окружающего воздуха (байт 0)
	if data[0] != 0xFF {
		tempRaw := data[0]
		temp := float64(tempRaw) - 40
		p.data.AmbientAirTemp = &temp
	}
}

// parseDM1 парсит активные диагностические коды (DM1)
func (p *J1939Protocol) parseDM1(data []byte, sa uint8) {
	p.parseDTCCodes(data, sa, true)
}

// parseDM2 парсит предыдущие диагностические коды (DM2)
func (p *J1939Protocol) parseDM2(data []byte, sa uint8) {
	p.parseDTCCodes(data, sa, false)
}

// parseDTCCodes парсит коды неисправности DTC из сообщений DM1/DM2
func (p *J1939Protocol) parseDTCCodes(data []byte, sa uint8, isActive bool) {
	if len(data) < 6 {
		return
	}

	// Сначала 2 байта лампочек и счетчика
	numberOfDTCs := (len(data) - 2) / 4
	offset := 2 // Пропускаем первые 2 байта (лампочки и счетчик)

	for i := 0; i < numberOfDTCs; i++ {
		if offset+4 > len(data) {
			break
		}

		// SPN (Suspect Parameter Number) - первые 3 байта
		spn := uint32(data[offset]) | uint32(data[offset+1])<<8 | (uint32(data[offset+2]&0x1F) << 16)

		// FMI (Failure Mode Identifier) - 5 бит 4-го байта
		fmi := int(data[offset+3] & 0x1F)

		// OC (Occurrence Count) - 3 старших бита 4-го байта
		oc := int((data[offset+3] & 0xE0) >> 5)

		dtcCode := protocol.DTCCode{
			MID:         int(sa),  // В J1939 используется SA вместо MID
			PID:         int(spn), // В J1939 используется SPN вместо PID
			FMI:         fmi,
			OC:          oc,
			Timestamp:   time.Now().Unix(),
			Description: p.getDTCDescription(sa, spn, fmi),
		}

		if isActive {
			// Проверяем, нет ли уже такого кода
			found := false
			for j, code := range p.data.ActiveDTCCodes {
				if code.MID == int(sa) && code.PID == int(spn) && code.FMI == fmi {
					// Обновляем существующий код
					p.data.ActiveDTCCodes[j] = dtcCode
					found = true
					break
				}
			}
			if !found {
				p.data.ActiveDTCCodes = append(p.data.ActiveDTCCodes, dtcCode)
			}
		} else {
			found := false
			for j, code := range p.data.PreviousDTCCodes {
				if code.MID == int(sa) && code.PID == int(spn) && code.FMI == fmi {
					p.data.PreviousDTCCodes[j] = dtcCode
					found = true
					break
				}
			}
			if !found {
				p.data.PreviousDTCCodes = append(p.data.PreviousDTCCodes, dtcCode)
			}
		}

		offset += 4 // Переходим к следующему DTC
	}
}

// getDTCDescription получает описание кода неисправности
func (p *J1939Protocol) getDTCDescription(sa uint8, spn uint32, fmi int) string {
	srcDesc := "Неизвестный модуль"
	if desc, ok := sourceDescriptions[sa]; ok {
		srcDesc = desc
	}

	fmiDesc := "Неизвестная неисправность"
	if desc, ok := fmiDescriptions[fmi]; ok {
		fmiDesc = desc
	}

	return fmt.Sprintf("Модуль: %s, SPN: %d, Режим: %s", srcDesc, spn, fmiDesc)
}
