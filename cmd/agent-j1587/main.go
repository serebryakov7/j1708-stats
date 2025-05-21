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

	"github.com/tarm/serial"

	"github.com/serebryakov7/j1708-stats/common"
	"github.com/serebryakov7/j1708-stats/pkg/mqtt"
)

// Настройки по умолчанию
const (
	defaultPortName         = "/dev/ttyUSB0"
	defaultBaudRate         = 9600
	defaultMqttBroker       = "tcp://localhost:1883"
	defaultMqttTopic        = "vehicle/data/j1587"
	defaultMqttDTCTopic     = "vehicle/dtc/j1587"
	defaultMqttCommandTopic = "vehicle/command/j1587"
	defaultUpdateInterval   = 10 * time.Second
)

var (
	portName         = flag.String("port", defaultPortName, "Последовательный порт для чтения данных")
	baudRate         = flag.Int("baud", defaultBaudRate, "Скорость передачи данных в бодах")
	mqttBroker       = flag.String("broker", defaultMqttBroker, "MQTT брокер")
	mqttTopic        = flag.String("topic", defaultMqttTopic, "MQTT топик для основных данных")
	mqttDTCTopic     = flag.String("dtc_topic", defaultMqttDTCTopic, "MQTT топик для кодов неисправностей (DTC)")
	mqttCommandTopic = flag.String("command_topic", defaultMqttCommandTopic, "MQTT топик для команд")
	updateInterval   = flag.Duration("interval", defaultUpdateInterval, "Интервал обновления MQTT в секундах")
)

func main() {
	flag.Parse()

	log.Println("Запуск агента J1587...")

	portConfig := &serial.Config{
		Name:        *portName,
		Baud:        *baudRate,
		ReadTimeout: time.Millisecond * 100,
	}
	port, err := serial.OpenPort(portConfig)
	if err != nil {
		log.Fatalf("Ошибка открытия порта %s: %v", *portName, err)
	}
	defer port.Close()

	bus, err := NewBus(port) // Обновлено для обработки ошибки из NewBus
	if err != nil {
		log.Fatalf("Ошибка инициализации Bus: %v", err)
	}
	defer bus.Close() // Добавлен вызов Close для Bus

	if err := bus.StartReading(); err != nil {
		log.Fatalf("Ошибка запуска чтения данных J1587: %v", err)
	}
	defer bus.StopReading()

	mqttConfig := mqtt.MQTTConfig{
		Broker:         *mqttBroker,
		ClientID:       "vehicle-data-j1587",
		Topic:          *mqttTopic,
		DTCTopic:       *mqttDTCTopic,
		CommandTopic:   *mqttCommandTopic,
		UpdateInterval: *updateInterval,
	}

	mqttClient := mqtt.NewClient(mqttConfig,
		func() json.Marshaler {
			return bus.GetData()
		},
		func(cmd common.ServerCommand) error { // Используем ссылку на новую функцию
			return handleMQTTCommand(bus, cmd)
		})

	if err := mqttClient.Connect(); err != nil {
		log.Fatalf("Ошибка подключения к MQTT: %v", err)
	}
	defer mqttClient.Disconnect()

	mqttClient.StartPublishing()
	defer mqttClient.StopPublishing()

	// Запускаем обработку DTC в Bus
	go bus.StartProcessingDTCs(mqttClient)

	log.Printf("Сбор и отправка данных J1587 запущены. Нажмите Ctrl+C для завершения.")

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	<-sigChan

	log.Println("Завершение работы агента J1587...")
}

func handleMQTTCommand(bus *Bus, cmd common.ServerCommand) error {
	log.Printf("Получена команда: %+v", cmd)

	switch cmd.Type {
	case "clear_dtc":
		var targetMID byte = 128 // MID по умолчанию
		if cmd.Params.TargetMID != nil {
			targetMID = *cmd.Params.TargetMID
		}

		if err := bus.ClearActiveDTCs(targetMID); err != nil {
			log.Printf("Ошибка выполнения команды сброса DTC: %v", err)
			return fmt.Errorf("ошибка сброса DTC для MID %d: %w", targetMID, err)
		}
		log.Printf("Команда сброса DTC для MID %d выполнена", targetMID)
		return nil
	default:
		log.Printf("Неизвестный тип команды: %s. Команда обработана успешно (действие по умолчанию).", cmd.Type)
		return nil
	}
}
