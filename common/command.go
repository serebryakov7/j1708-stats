package common

// CommandType определяет тип команды от сервера.
type CommandType string

const (
	// CommandTypeClearDTCs предписывает сбросить активные коды неисправностей.
	CommandTypeClearDTCs CommandType = "clear_dtcs"
	// Другие типы команд могут быть добавлены здесь
)

// ServerCommand представляет команду, полученную от сервера через MQTT.
type ServerCommand struct {
	Type   CommandType   `json:"type"`
	Params CommandParams `json:"params,omitempty"`
}

// CommandParams содержит параметры для различных команд.
// Используйте указатели, чтобы опускать незаполненные поля в JSON.
type CommandParams struct {
	// TargetMID используется для команд, специфичных для модуля (например, J1587).
	// Это может быть идентификатор модуля (MID) для J1587 или адрес источника для J1939.
	TargetMID *byte `json:"target_mid,omitempty"`
	// SPN и FMI могут использоваться для более специфичных команд, связанных с DTC.
	SPN *int `json:"spn,omitempty"`
	FMI *int `json:"fmi,omitempty"`
	// Другие параметры для других команд
}

// CommandAck представляет подтверждение выполнения команды.
type CommandAck struct {
	CommandID string `json:"command_id"` // Идентификатор исходной команды, если есть
	Success   bool   `json:"success"`
	Message   string `json:"message,omitempty"`
}
