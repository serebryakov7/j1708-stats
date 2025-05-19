package protocol

import (
	"time"

	"github.com/tarm/serial"
)

// ProtocolType определяет тип протокола
type ProtocolType string

const (
	// J1587Protocol - протокол J1587/J1708
	J1587Protocol ProtocolType = "j1587"
	// J1939Protocol - протокол J1939
	J1939Protocol ProtocolType = "j1939"
)

// VehicleData представляет универсальную структуру данных транспортного средства
type VehicleData interface {
	// GetTimestamp возвращает временную метку данных
	GetTimestamp() time.Time
	// SetTimestamp устанавливает временную метку данных
	SetTimestamp(timestamp time.Time)
	// ToJSON сериализует данные в JSON формат
	ToJSON() ([]byte, error)
}

// DTCCode представляет код неисправности (DTC)
type DTCCode struct {
	MID         int    `json:"mid"`                   // Message Identifier
	PID         int    `json:"pid"`                   // Parameter Identifier
	FMI         int    `json:"fmi"`                   // Failure Mode Identifier
	OC          int    `json:"oc"`                    // Occurrence Count
	Timestamp   int64  `json:"timestamp"`             // Время обнаружения
	Description string `json:"description,omitempty"` // Описание ошибки если известно
}

// Protocol определяет интерфейс для разных протоколов обмена данными
type Protocol interface {
	// Initialize инициализирует протокол
	Initialize(port *serial.Port) error
	// StartReading начинает чтение данных с порта
	StartReading() error
	// StopReading останавливает чтение данных
	StopReading() error
	// GetData возвращает актуальные данные транспортного средства
	GetData() VehicleData
	// GetProtocolType возвращает тип протокола
	GetProtocolType() ProtocolType
}

// ProtocolFactory создает соответствующий протокол по его типу
func ProtocolFactory(protocolType ProtocolType) Protocol {
	switch protocolType {
	case J1587Protocol:
		return NewJ1587Protocol()
	case J1939Protocol:
		return NewJ1939Protocol()
	default:
		return nil
	}
}

// Заглушки для функций фабрики, которые будут реализованы в пакетах j1587 и j1939
var (
	NewJ1587Protocol func() Protocol
	NewJ1939Protocol func() Protocol
)
