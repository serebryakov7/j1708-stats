package j1587

import (
	"encoding/json"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/tarm/serial"

	"github.com/serebryakov7/j1708-stats/internal/protocol"
)

const (
	interFrameGap = 4 * time.Millisecond
)

// J1587 Parameter IDs
const (
	PID_VEHICLE_SPEED         = 84
	PID_ENGINE_RPM            = 190
	PID_COOLANT_TEMP          = 110
	PID_OIL_PRESSURE          = 100
	PID_ENGINE_LOAD           = 91
	PID_FUEL_LEVEL            = 96
	PID_BATTERY_VOLTAGE       = 168
	PID_AMBIENT_TEMP          = 171
	PID_TOTAL_DISTANCE        = 245
	PID_ACTIVE_DTC            = 194
	PID_PREVIOUSLY_ACTIVE_DTC = 195
)

// J1587Data содержит все данные автомобиля, собираемые с шины J1587
type J1587Data struct {
	Timestamp         time.Time          `json:"timestamp"`
	Speed             *float64           `json:"speed,omitempty"`
	EngineRPM         *float64           `json:"engine_rpm,omitempty"`
	EngineCoolantTemp *float64           `json:"coolant_temp,omitempty"`
	EngineOilPressure *float64           `json:"oil_pressure,omitempty"`
	EngineLoad        *float64           `json:"engine_load,omitempty"`
	FuelLevel         *float64           `json:"fuel_level,omitempty"`
	BatteryVoltage    *float64           `json:"battery_voltage,omitempty"`
	AmbientAirTemp    *float64           `json:"ambient_temp,omitempty"`
	TotalDistance     *float64           `json:"total_distance,omitempty"`
	ActiveDTCCodes    []protocol.DTCCode `json:"active_dtc_codes,omitempty"`
	PreviousDTCCodes  []protocol.DTCCode `json:"previous_dtc_codes,omitempty"`
	RawFrames         map[int][]byte     `json:"-"` // Хранит последние необработанные фреймы по MID
	mutex             sync.Mutex         `json:"-"`
}

// Реализация методов интерфейса VehicleData
func (d *J1587Data) GetTimestamp() time.Time {
	return d.Timestamp
}

func (d *J1587Data) SetTimestamp(timestamp time.Time) {
	d.Timestamp = timestamp
}

func (d *J1587Data) ToJSON() ([]byte, error) {
	return json.Marshal(d)
}

// J1587Protocol реализует интерфейс Protocol для протокола J1587
type J1587Protocol struct {
	port      *serial.Port
	data      *J1587Data
	frames    chan []byte
	stopChan  chan struct{}
	isRunning bool
}

// J1587 MIDs для различных электронных модулей
var midDescriptions = map[int]string{
	0:   "Общее назначение",
	128: "Двигатель #1",
	130: "Трансмиссия #1",
	136: "Тормозная система",
	140: "Приборная панель",
	141: "Электронный блок управления кузовом",
	143: "Бортовой компьютер",
	144: "Шасси",
	155: "ABS",
	// Можно добавить больше MID согласно спецификации
}

// Описания кодов неисправности FMI (Failure Mode Identifier)
var fmiDescriptions = map[int]string{
	0:  "Данные выше нормы",
	1:  "Данные ниже нормы",
	2:  "Некорректные данные",
	3:  "Электрическая неисправность",
	4:  "Электрическая неисправность",
	5:  "Электрическая неисправность",
	6:  "Электрическая неисправность",
	7:  "Механическая неисправность",
	8:  "Нестандартная частота или скважность сигнала",
	9:  "Нестандартная скорость обновления",
	10: "Нестандартная скорость изменения",
	11: "Неопределенная неисправность",
	12: "Повреждение устройства или компонента",
	13: "Некорректная калибровка",
	14: "Особая инструкция",
	15: "Данные выше нормы (наименьшая серьезность)",
}

// NewJ1587 создает новый экземпляр J1587Protocol
func NewJ1587() protocol.Protocol {
	protocol.NewJ1587Protocol = func() protocol.Protocol {
		return &J1587Protocol{
			data: &J1587Data{
				RawFrames: make(map[int][]byte),
			},
			frames:   make(chan []byte),
			stopChan: make(chan struct{}),
		}
	}
	return protocol.NewJ1587Protocol()
}

// Initialize инициализирует протокол
func (p *J1587Protocol) Initialize(port *serial.Port) error {
	p.port = port
	return nil
}

// StartReading начинает чтение данных с порта
func (p *J1587Protocol) StartReading() error {
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
func (p *J1587Protocol) StopReading() error {
	if !p.isRunning {
		return nil
	}

	close(p.stopChan)
	p.isRunning = false
	return nil
}

// GetData возвращает актуальные данные транспортного средства
func (p *J1587Protocol) GetData() protocol.VehicleData {
	p.data.mutex.Lock()
	defer p.data.mutex.Unlock()
	return p.data
}

// GetProtocolType возвращает тип протокола
func (p *J1587Protocol) GetProtocolType() protocol.ProtocolType {
	return protocol.J1587Protocol
}

// readFrames читает фреймы из последовательного порта
func (p *J1587Protocol) readFrames() {
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
func (p *J1587Protocol) processFrames() {
	for {
		select {
		case <-p.stopChan:
			return
		case frame := <-p.frames:
			if len(frame) < 1 {
				continue
			}

			mid := frame[0]
			data := frame[1:]

			// Выводим фрейм для отладки
			fmt.Printf("J1587 FRAME: MID=%d DATA=% X\n", mid, data)

			// Парсим фрейм J1587
			p.parseFrame(frame)
		}
	}
}

// parseFrame разбирает фрейм J1587
func (p *J1587Protocol) parseFrame(frame []byte) {
	if len(frame) < 2 {
		return
	}

	mid := int(frame[0])
	p.data.mutex.Lock()
	defer p.data.mutex.Unlock()

	// Сохраняем сырые данные для каждого MID
	p.data.RawFrames[mid] = make([]byte, len(frame))
	copy(p.data.RawFrames[mid], frame)

	data := frame[1:]
	if len(data) == 0 {
		return
	}

	pid := int(data[0])
	paramData := data[1:]

	// Парсинг различных параметров по их PID
	switch pid {
	case PID_VEHICLE_SPEED:
		if len(paramData) >= 1 {
			speed := float64(paramData[0])
			p.data.Speed = &speed
		}
	case PID_ENGINE_RPM:
		if len(paramData) >= 2 {
			rpm := float64((int(paramData[0])*256 + int(paramData[1])) / 8)
			p.data.EngineRPM = &rpm
		}
	case PID_COOLANT_TEMP:
		if len(paramData) >= 1 {
			temp := float64(int(paramData[0]) - 40) // Коррекция смещения по J1587
			p.data.EngineCoolantTemp = &temp
		}
	case PID_OIL_PRESSURE:
		if len(paramData) >= 1 {
			pressure := float64(paramData[0]) * 4.0
			p.data.EngineOilPressure = &pressure
		}
	case PID_ENGINE_LOAD:
		if len(paramData) >= 1 {
			load := float64(paramData[0])
			p.data.EngineLoad = &load
		}
	case PID_FUEL_LEVEL:
		if len(paramData) >= 1 {
			level := float64(paramData[0]) / 2.55 // Преобразуем в процент
			p.data.FuelLevel = &level
		}
	case PID_BATTERY_VOLTAGE:
		if len(paramData) >= 1 {
			voltage := float64(paramData[0]) * 0.1
			p.data.BatteryVoltage = &voltage
		}
	case PID_AMBIENT_TEMP:
		if len(paramData) >= 1 {
			temp := float64(int(paramData[0]) - 40)
			p.data.AmbientAirTemp = &temp
		}
	case PID_TOTAL_DISTANCE:
		if len(paramData) >= 4 {
			distance := float64(
				int(paramData[0])<<24|
					int(paramData[1])<<16|
					int(paramData[2])<<8|
					int(paramData[3])) * 0.1 // км
			p.data.TotalDistance = &distance
		}
	case PID_ACTIVE_DTC:
		p.parseDTCCodes(mid, paramData, true)
	case PID_PREVIOUSLY_ACTIVE_DTC:
		p.parseDTCCodes(mid, paramData, false)
	}
}

// parseDTCCodes парсит коды неисправности DTC
func (p *J1587Protocol) parseDTCCodes(mid int, data []byte, isActive bool) {
	if len(data) < 2 {
		return
	}

	// У каждой записи DTC должен быть как минимум PID и FMI
	pid := int(data[0])
	fmi := int(data[1]) & 0x1F       // 5 младших битов - FMI
	oc := (int(data[1]) & 0xE0) >> 5 // 3 старших бита - OC (Occurrence Count)

	dtcCode := protocol.DTCCode{
		MID:         mid,
		PID:         pid,
		FMI:         fmi,
		OC:          oc,
		Timestamp:   time.Now().Unix(),
		Description: p.getDTCDescription(mid, pid, fmi),
	}

	if isActive {
		// Проверяем, нет ли уже такого кода
		for i, code := range p.data.ActiveDTCCodes {
			if code.MID == mid && code.PID == pid && code.FMI == fmi {
				// Обновляем существующий код
				p.data.ActiveDTCCodes[i] = dtcCode
				return
			}
		}
		p.data.ActiveDTCCodes = append(p.data.ActiveDTCCodes, dtcCode)
	} else {
		for i, code := range p.data.PreviousDTCCodes {
			if code.MID == mid && code.PID == pid && code.FMI == fmi {
				p.data.PreviousDTCCodes[i] = dtcCode
				return
			}
		}
		p.data.PreviousDTCCodes = append(p.data.PreviousDTCCodes, dtcCode)
	}
}

// getDTCDescription получает описание кода неисправности
func (p *J1587Protocol) getDTCDescription(mid, pid, fmi int) string {
	midDesc := "Неизвестный модуль"
	if desc, ok := midDescriptions[mid]; ok {
		midDesc = desc
	}

	fmiDesc := "Неизвестная неисправность"
	if desc, ok := fmiDescriptions[fmi]; ok {
		fmiDesc = desc
	}

	return fmt.Sprintf("Модуль: %s, Параметр ID: %d, Режим: %s", midDesc, pid, fmiDesc)
}
