// go:build linux
//go:build linux
// +build linux

package main

import (
	"encoding/json"
	"sync"
	"time"
)

// ProtectedData инкапсулирует карту данных J1939 и мьютекс для безопасного доступа.
type ProtectedData struct {
	mutex sync.RWMutex
	Data  map[string]any // Хранилище для разобранных данных J1939: имя метрики -> значение
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
// Сериализует только карту Data.
func (pd *ProtectedData) MarshalJSON() ([]byte, error) {
	pd.mutex.RLock()
	defer pd.mutex.RUnlock()

	// Копируем данные для избежания удержания блокировки во время маршалинга
	// и для добавления временной метки непосредственно перед отправкой.
	dataToMarshal := make(map[string]any, len(pd.Data)+1)
	for k, v := range pd.Data {
		dataToMarshal[k] = v
	}
	// Добавляем временную метку каждый раз при сериализации
	dataToMarshal["timestamp"] = time.Now().UTC().Format(time.RFC3339Nano)

	return json.Marshal(dataToMarshal)
}

// Copy создает глубокую копию данных из ProtectedData для безопасной передачи.
// Возвращает json.Marshaler, который при вызове MarshalJSON вернет копию данных.
func (pd *ProtectedData) Copy() json.Marshaler {
	pd.mutex.RLock()
	defer pd.mutex.RUnlock()

	// Создаем копию карты для передачи
	copiedData := make(map[string]any, len(pd.Data))
	for key, value := range pd.Data {
		// Для простых типов прямое присваивание достаточно для копии.
		// Если бы значения были указателями или сложными структурами, потребовалось бы глубокое копирование.
		copiedData[key] = value
	}
	// Возвращаем обертку, которая будет использовать скопированные данные при маршалинга
	return &copiedDataMarshaler{data: copiedData}
}

// copiedDataMarshaler вспомогательный тип для реализации json.Marshaler на основе скопированной карты.
type copiedDataMarshaler struct {
	data map[string]any
}

func (m *copiedDataMarshaler) MarshalJSON() ([]byte, error) {
	// Добавляем временную метку в копию данных непосредственно перед маршалингом
	dataToMarshal := make(map[string]any, len(m.data)+1)
	for k, v := range m.data {
		dataToMarshal[k] = v
	}
	dataToMarshal["timestamp"] = time.Now().UTC().Format(time.RFC3339Nano)
	return json.Marshal(dataToMarshal)
}

// J1939Data теперь псевдоним для ProtectedData для обратной совместимости в некоторых местах,
// но основная работа будет с ProtectedData.
// Или лучше полностью заменить J1939Data на ProtectedData в bus.go и других файлах.
// Пока оставим так, чтобы минимизировать первоначальные изменения в bus.go,
// но в перспективе лучше использовать ProtectedData напрямую.
type J1939Data = ProtectedData

// NewJ1939Data теперь вызывает NewProtectedData.
func NewJ1939Data() *J1939Data {
	return NewProtectedData()
}
