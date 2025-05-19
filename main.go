package main

import (
	"flag"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/tarm/serial"

	"github.com/serebryakov7/j1708-stats/internal/j1587"
	"github.com/serebryakov7/j1708-stats/internal/j1939"
	"github.com/serebryakov7/j1708-stats/internal/mqtt"
	"github.com/serebryakov7/j1708-stats/internal/protocol"
)

// Настройки по умолчанию
const (
	defaultPortName       = "/dev/ttyUSB0"
	defaultBaudRate       = 9600
	defaultProtocol       = "j1587"
	defaultMqttBroker     = "tcp://localhost:1883"
	defaultMqttTopic      = "vehicle/data"
	defaultUpdateInterval = 10 * time.Second
)

var (
	portName       = flag.String("port", defaultPortName, "Последовательный порт для чтения данных")
	baudRate       = flag.Int("baud", defaultBaudRate, "Скорость передачи данных в бодах")
	protocolType   = flag.String("protocol", defaultProtocol, "Протокол (j1587 или j1939)")
	mqttBroker     = flag.String("broker", defaultMqttBroker, "MQTT брокер")
	mqttTopic      = flag.String("topic", defaultMqttTopic, "MQTT топик")
	updateInterval = flag.Duration("interval", defaultUpdateInterval, "Интервал обновления MQTT в секундах")
)

func main() {
	flag.Parse()

	// Нормализуем строку протокола
	protocolName := strings.ToLower(*protocolType)

	var protocolInstance protocol.Protocol
	// Инициализируем соответствующий протокол
	switch protocolName {
	case "j1587":
		log.Println("Используется протокол J1587/J1708")
		protocolInstance = j1587.NewJ1587()
	case "j1939":
		log.Println("Используется протокол J1939")
		protocolInstance = j1939.NewJ1939()
	default:
		log.Fatalf("Неподдерживаемый протокол: %s. Используйте j1587 или j1939", protocolName)
	}

	// Открываем последовательный порт
	portConfig := &serial.Config{
		Name:        *portName,
		Baud:        *baudRate,
		ReadTimeout: time.Millisecond * 100,
	}

	port, err := serial.OpenPort(portConfig)
	if err != nil {
		log.Fatalf("Ошибка открытия порта: %v", err)
	}
	defer port.Close()

	// Инициализируем протокол
	if err := protocolInstance.Initialize(port); err != nil {
		log.Fatalf("Ошибка инициализации протокола: %v", err)
	}

	// Начинаем чтение данных
	if err := protocolInstance.StartReading(); err != nil {
		log.Fatalf("Ошибка запуска чтения данных: %v", err)
	}
	defer protocolInstance.StopReading()

	// Настраиваем MQTT клиент
	mqttConfig := mqtt.MQTTConfig{
		Broker:         *mqttBroker,
		ClientID:       "vehicle-data-" + strings.Replace(protocolName, "/", "-", -1),
		Topic:          *mqttTopic,
		UpdateInterval: *updateInterval,
	}

	mqttClient := mqtt.NewClient(mqttConfig, func() protocol.VehicleData {
		return protocolInstance.GetData()
	})

	if err := mqttClient.Connect(); err != nil {
		log.Fatalf("Ошибка подключения к MQTT: %v", err)
	}
	defer mqttClient.Disconnect()

	// Начинаем публикацию данных
	mqttClient.StartPublishing()
	defer mqttClient.StopPublishing()

	log.Printf("Сбор и отправка данных %s запущены. Нажмите Ctrl+C для завершения", protocolName)

	// Ожидаем сигнал для завершения работы
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	<-sigChan

	log.Println("Завершение работы...")
}
