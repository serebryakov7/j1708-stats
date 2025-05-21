package mqtt

import (
	"encoding/json"
	"log"
	"time"

	mqtt "github.com/eclipse/paho.mqtt.golang"

	"github.com/serebryakov7/j1708-stats/common"
)

const (
	DefaultUpdateInterval = 10 * time.Second
	DefaultBroker         = "tcp://localhost:1883"
	DefaultClientID       = "vehicle-data-collector"
	DefaultTopic          = "vehicle/data"
)

// MQTTConfig содержит настройки для MQTT клиента
type MQTTConfig struct {
	Broker         string
	ClientID       string
	Topic          string
	DTCTopic       string // Топик для отправки DTC
	CommandTopic   string // Топик для получения команд
	UpdateInterval time.Duration
}

// MQTTClient представляет MQTT клиент для отправки данных и получения команд
type MQTTClient struct {
	config     MQTTConfig
	client     mqtt.Client
	stopChan   chan struct{}
	dataSource func() json.Marshaler
	// commandHandler - функция обратного вызова для обработки команд
	commandHandler func(cmd common.ServerCommand) error
}

// NewClient создает новый MQTT клиент
func NewClient(config MQTTConfig, dataSource func() json.Marshaler, cmdHandler func(cmd common.ServerCommand) error) *MQTTClient {
	return &MQTTClient{
		config:         config,
		stopChan:       make(chan struct{}),
		dataSource:     dataSource,
		commandHandler: cmdHandler,
	}
}

// Connect устанавливает соединение с MQTT брокером
func (c *MQTTClient) Connect() error {
	opts := mqtt.NewClientOptions()
	opts.AddBroker(c.config.Broker)
	opts.SetClientID(c.config.ClientID)
	opts.SetAutoReconnect(true)
	opts.SetOnConnectHandler(func(client mqtt.Client) {
		log.Println("Подключено к MQTT брокеру")
		// Подписываемся на топик команд после успешного подключения
		c.subscribeToCommands()
	})
	opts.SetConnectionLostHandler(func(client mqtt.Client, err error) {
		log.Printf("Соединение с MQTT брокером потеряно: %v", err)
	})

	c.client = mqtt.NewClient(opts)
	if token := c.client.Connect(); token.Wait() && token.Error() != nil {
		return token.Error()
	}

	return nil
}

// StartPublishing начинает периодическую отправку данных
func (c *MQTTClient) StartPublishing() {
	ticker := time.NewTicker(c.config.UpdateInterval)
	defer ticker.Stop()

	log.Printf("Начало публикации данных в MQTT на топик %s с интервалом %v", c.config.Topic, c.config.UpdateInterval)

	go func() {
		for {
			select {
			case <-c.stopChan:
				return
			case <-ticker.C:
				c.publishData()
			}
		}
	}()
}

// StopPublishing останавливает публикацию данных
func (c *MQTTClient) StopPublishing() {
	close(c.stopChan)
}

// Disconnect отключается от MQTT брокера
func (c *MQTTClient) Disconnect() {
	if c.client != nil && c.client.IsConnected() {
		c.client.Disconnect(250)
	}
}

// publishData публикует данные в MQTT
func (c *MQTTClient) publishData() {
	vehicleData := c.dataSource()
	if vehicleData == nil {
		log.Println("Нет данных для публикации")
		return
	}

	data, err := vehicleData.MarshalJSON()
	if err != nil {
		log.Printf("Ошибка сериализации данных: %v", err)
		return
	}

	token := c.client.Publish(c.config.Topic, 0, false, data)
	if token.Wait() && token.Error() != nil {
		log.Printf("Ошибка отправки данных в MQTT: %v", token.Error())
	} else {
		log.Printf("Данные отправлены в MQTT (%d байт)", len(data))
	}
}

// subscribeToCommands подписывается на топик команд от сервера.
func (c *MQTTClient) subscribeToCommands() {
	commandTopic := c.config.CommandTopic
	if commandTopic == "" {
		log.Println("Топик для команд не указан, подписка не будет выполнена.")
		return
	}

	token := c.client.Subscribe(commandTopic, 1, c.handleIncomingCommand)
	go func() {
		<-token.Done()
		if token.Error() != nil {
			log.Printf("Ошибка подписки на топик команд %s: %v", commandTopic, token.Error())
		} else {
			log.Printf("Успешно подписан на топик команд: %s", commandTopic)
		}
	}()
}

// handleIncomingCommand обрабатывает входящие сообщения из топика команд.
func (c *MQTTClient) handleIncomingCommand(client mqtt.Client, msg mqtt.Message) {
	log.Printf("Получена команда из топика %s: %s", msg.Topic(), string(msg.Payload()))

	var cmd common.ServerCommand
	if err := json.Unmarshal(msg.Payload(), &cmd); err != nil {
		log.Printf("Ошибка десериализации команды: %v. Сообщение: %s", err, string(msg.Payload()))
		return
	}

	if c.commandHandler != nil {
		if err := c.commandHandler(cmd); err != nil {
			log.Printf("Ошибка обработки команды %s: %v", cmd.Type, err)
		}
	} else {
		log.Println("Обработчик команд не настроен.")
	}
}

// PublishDTC публикует один DTC в MQTT
func (c *MQTTClient) PublishDTC(dtc common.DTCCode) {
	if !c.client.IsConnected() {
		log.Println("MQTT клиент не подключен, DTC не будет отправлен")
		return
	}

	data, err := json.Marshal(dtc)
	if err != nil {
		log.Printf("Ошибка сериализации DTC: %v", err)
		return
	}

	dtcTopic := c.config.DTCTopic
	if dtcTopic == "" {
		dtcTopic = c.config.Topic + "/dtc" // Топик по умолчанию, если не задан
	}

	token := c.client.Publish(dtcTopic, 0, false, data)
	if token.Wait() && token.Error() != nil {
		log.Printf("Ошибка отправки DTC в MQTT: %v", token.Error())
	} else {
		log.Printf("DTC %d отправлен в MQTT на топик %s (%d байт)", dtc.SPN, dtcTopic, len(data))
	}
}
