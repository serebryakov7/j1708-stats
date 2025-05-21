package main

import (
	"encoding/json"
	"sync"
	"time"
)

// ProtectedData инкапсулирует карту данных J1587 и мьютекс для безопасного доступа.
type ProtectedData struct {
	mutex sync.RWMutex
	Data  map[string]any // Хранилище для разобранных данных J1587: имя метрики -> значение
}

// NewProtectedData создает новый экземпляр ProtectedData.
func NewProtectedData() *ProtectedData {
	return &ProtectedData{
		Data: make(map[string]any),
	}
}

// Set устанавливает значение в карте данных под защитой мьютекса.
func (pd *ProtectedData) Set(key string, value any) {
	pd.mutex.Lock()
	defer pd.mutex.Unlock()
	pd.Data[key] = value
}

// Get извлекает значение из карты данных под защитой мьютекса.
func (pd *ProtectedData) Get(key string) (any, bool) {
	pd.mutex.RLock()
	defer pd.mutex.RUnlock()
	val, ok := pd.Data[key]
	return val, ok
}

// MarshalJSON реализует интерфейс json.Marshaler для ProtectedData.
// Сериализует карту Data с добавлением временной метки.
func (pd *ProtectedData) MarshalJSON() ([]byte, error) {
	pd.mutex.RLock()
	defer pd.mutex.RUnlock()

	dataToMarshal := make(map[string]any, len(pd.Data)+1)
	for k, v := range pd.Data {
		dataToMarshal[k] = v
	}
	dataToMarshal["timestamp"] = time.Now().UTC().Format(time.RFC3339Nano)

	return json.Marshal(dataToMarshal)
}

// Copy создает json.Marshaler, который при вызове MarshalJSON вернет копию данных
// с актуальной временной меткой.
func (pd *ProtectedData) Copy() json.Marshaler {
	pd.mutex.RLock()
	defer pd.mutex.RUnlock()

	copiedData := make(map[string]any, len(pd.Data))
	for key, value := range pd.Data {
		copiedData[key] = value
	}
	return &copiedDataMarshaler{data: copiedData}
}

// copiedDataMarshaler вспомогательный тип для реализации json.Marshaler на основе скопированной карты.
type copiedDataMarshaler struct {
	data map[string]any
}

// MarshalJSON для copiedDataMarshaler добавляет временную метку к скопированным данным.
func (m *copiedDataMarshaler) MarshalJSON() ([]byte, error) {
	dataToMarshal := make(map[string]any, len(m.data)+1)
	for k, v := range m.data {
		dataToMarshal[k] = v
	}
	dataToMarshal["timestamp"] = time.Now().UTC().Format(time.RFC3339Nano)
	return json.Marshal(dataToMarshal)
}

// J1587Data теперь псевдоним для ProtectedData.
type J1587Data = ProtectedData

// NewJ1587Data теперь вызывает NewProtectedData.
func NewJ1587Data() *J1587Data {
	return NewProtectedData()
}
