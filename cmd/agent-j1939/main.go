// go:build linux
//go:build linux
// +build linux

package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/serebryakov7/j1708-stats/pkg/mqtt"
	"github.com/serebryakov7/j1708-stats/pkg/storage" // Добавлен импорт для storage
	bolt "go.etcd.io/bbolt"
)

// Настройки по умолчанию
const (
	defaultMqttBroker     = "tcp://localhost:1883"
	defaultMqttTopic      = "vehicle/data/j1939"
	defaultMqttDTCTopic   = "vehicle/dtc/j1939"
	defaultUpdateInterval = 10 * time.Second
	defaultCanInterface   = "can0"
	defaultDbPath         = "j1939_dtc.db" // Путь к файлу БД для DTC J1939
)

var (
	mqttBroker     = flag.String("broker", defaultMqttBroker, "MQTT брокер")
	mqttTopic      = flag.String("topic", defaultMqttTopic, "MQTT топик для основных данных")
	mqttDTCTopic   = flag.String("dtc_topic", defaultMqttDTCTopic, "MQTT топик для кодов неисправностей (DTC)")
	updateInterval = flag.Duration("interval", defaultUpdateInterval, "Интервал обновления MQTT в секундах")
	canInterface   = flag.String("can-if", defaultCanInterface, "CAN interface name (e.g., can0, vcan0)")
	dbPath         = flag.String("dbpath", defaultDbPath, "Path to the bbolt database file for J1939 DTCs")
)

func main() {
	flag.Parse()
	log.SetOutput(os.Stdout)
	log.SetFlags(log.LstdFlags | log.Lmicroseconds)
	log.Printf("Запуск агента J1939 на интерфейсе %s...", *canInterface)

	// Инициализация bbolt DB
	// Переменная db должна быть типа *bolt.DB, который возвращает storage.OpenDB
	var db *bolt.DB // Объявляем переменную db здесь
	var errDbOpen error
	db, errDbOpen = storage.OpenDB(*dbPath) // Используем путь из флага
	if errDbOpen != nil {
		log.Fatalf("Ошибка открытия/создания bbolt DB по пути %s: %v", *dbPath, errDbOpen)
	}
	defer func() {
		if db != nil { // Проверяем, что db не nil перед закрытием
			if err := db.Close(); err != nil {
				log.Printf("Ошибка закрытия bbolt DB: %v", err)
			}
		}
	}()
	log.Printf("Bbolt DB для J1939 DTC инициализирована: %s", *dbPath)

	// Init CAN bus
	// Передаем db в NewBus, который затем передаст его в NewFrameProcessor
	bus, err := NewBus(*canInterface, db) // Изменено: передаем db
	if err != nil {
		log.Fatalf("Ошибка инициализации шины J1939: %v", err)
	}

	bus.Start()

	// Init MQTT
	mqttConfig := mqtt.MQTTConfig{
		Broker:         *mqttBroker,
		ClientID:       fmt.Sprintf("j1939-agent-%s-%d", *canInterface, time.Now().UnixNano()), // Более уникальный ClientID
		Topic:          *mqttTopic,
		DTCTopic:       *mqttDTCTopic,
		UpdateInterval: *updateInterval,
	}

	mqttClient := mqtt.NewClient(mqttConfig, func() json.Marshaler {
		return bus.GetData() // bus.GetData() возвращает *main.J1939Data, который реализует json.Marshaler
	}, nil)

	if err := mqttClient.Connect(); err != nil {
		log.Fatalf("Ошибка подключения к MQTT: %v", err)
	}
	// defer mqttClient.Disconnect() вызывается после выхода из main

	mqttClient.StartPublishing() // Запускаем публикацию основных данных

	// Канал для координации завершения горутин
	done := make(chan struct{})

	// Запуск горутины для отправки DTC по MQTT
	go func() {
		defer func() { log.Println("Горутина отправки DTC завершена.") }()
		log.Println("Горутина отправки DTC запущена.")
		for {
			select {
			case dtc, ok := <-bus.GetDTCChannel():
				if !ok {
					log.Println("Канал DTC закрыт, выход из горутины отправки DTC.")
					return
				}
				mqttClient.PublishDTC(dtc)
			case <-done: // Сигнал для завершения этой горутины
				log.Println("Получен сигнал 'done', выход из горутины отправки DTC.")
				return
			}
		}
	}()

	log.Println("Агент J1939 запущен. Нажмите Ctrl+C для выхода.")
	// Ожидание сигнала завершения
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	// Блокируемся здесь до получения сигнала
	sig := <-sigChan
	log.Printf("Получен сигнал %s. Завершение работы...", sig)

	// Сигнализируем горутинам о завершении
	log.Println("Отправка сигнала 'done' в горутины...")
	close(done)

	// Останавливаем MQTT клиент
	log.Println("Остановка MQTT клиента...")
	mqttClient.StopPublishing() // Останавливаем периодическую публикацию
	mqttClient.Disconnect()
	log.Println("MQTT клиент остановлен.")

	// Останавливаем шину CAN
	log.Println("Остановка шины J1939...")
	if err := bus.Stop(); err != nil {
		log.Printf("Ошибка при остановке шины J1939: %v", err)
	}
	log.Println("Шина J1939 остановлена.")

	log.Println("Агент J1939 завершил работу.")
}
