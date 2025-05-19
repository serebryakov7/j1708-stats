package mqtt

import (
	"log"
	"time"

	mqtt "github.com/eclipse/paho.mqtt.golang"
	"github.com/serebryakov7/j1708-stats/internal/protocol"
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
	UpdateInterval time.Duration
}

// Client представляет MQTT клиент для отправки данных
type Client struct {
	config     MQTTConfig
	client     mqtt.Client
	stopChan   chan struct{}
	dataSource func() protocol.VehicleData
}

// NewClient создает новый MQTT клиент
func NewClient(config MQTTConfig, dataSource func() protocol.VehicleData) *Client {
	return &Client{
		config:     config,
		stopChan:   make(chan struct{}),
		dataSource: dataSource,
	}
}

// Connect устанавливает соединение с MQTT брокером
func (c *Client) Connect() error {
	opts := mqtt.NewClientOptions()
	opts.AddBroker(c.config.Broker)
	opts.SetClientID(c.config.ClientID)
	opts.SetAutoReconnect(true)
	opts.SetOnConnectHandler(func(client mqtt.Client) {
		log.Println("Подключено к MQTT брокеру")
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
func (c *Client) StartPublishing() {
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
func (c *Client) StopPublishing() {
	close(c.stopChan)
}

// Disconnect отключается от MQTT брокера
func (c *Client) Disconnect() {
	if c.client != nil && c.client.IsConnected() {
		c.client.Disconnect(250)
	}
}

// publishData публикует данные в MQTT
func (c *Client) publishData() {
	vehicleData := c.dataSource()
	if vehicleData == nil {
		log.Println("Нет данных для публикации")
		return
	}

	vehicleData.SetTimestamp(time.Now())
	data, err := vehicleData.ToJSON()
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
