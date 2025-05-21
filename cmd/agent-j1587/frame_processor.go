package main

import (
	"fmt"
	"log"
	"time"

	"github.com/serebryakov7/j1708-stats/common"
)

// parseFrame разбирает фрейм J1587
func (p *Bus) parseFrame(frame []byte) {
	if len(frame) < 2 {
		return
	}

	mid := int(frame[0])
	data := frame[1:]

	if len(data) == 0 {
		return
	}

	pid := int(data[0])
	paramData := data[1:]

	// Мьютекс теперь управляется внутри методов Set/Get ProtectedData (J1587Data)
	// p.data.mutex.Lock() и defer p.data.mutex.Unlock() здесь больше не нужны.

	// Парсинг различных параметров по их PID
	switch pid {
	case PID_VEHICLE_SPEED:
		if len(paramData) >= 1 {
			speed := float64(paramData[0])
			p.data.Set("Speed", speed) // Используем Set
		}
	case PID_ENGINE_RPM:
		if len(paramData) >= 2 {
			rpm := float64((int(paramData[0])*256 + int(paramData[1])) / 8)
			p.data.Set("EngineRPM", rpm) // Используем Set
		}
	case PID_COOLANT_TEMP:
		if len(paramData) >= 1 {
			temp := float64(int(paramData[0]) - 40) // Коррекция смещения по J1587
			p.data.Set("EngineCoolantTemp", temp)   // Используем Set
		}
	case PID_OIL_PRESSURE:
		if len(paramData) >= 1 {
			pressure := float64(paramData[0]) * 4.0
			p.data.Set("EngineOilPressure", pressure) // Используем Set
		}
	case PID_ENGINE_LOAD:
		if len(paramData) >= 1 {
			load := float64(paramData[0])
			p.data.Set("EngineLoad", load) // Используем Set
		}
	case PID_FUEL_LEVEL:
		if len(paramData) >= 1 {
			level := float64(paramData[0]) / 2.55 // Преобразуем в процент
			p.data.Set("FuelLevel", level)        // Используем Set
		}
	case PID_BATTERY_VOLTAGE:
		if len(paramData) >= 1 {
			voltage := float64(paramData[0]) * 0.1
			p.data.Set("BatteryVoltage", voltage) // Используем Set
		}
	case PID_AMBIENT_TEMP:
		if len(paramData) >= 1 {
			temp := float64(int(paramData[0]) - 40)
			p.data.Set("AmbientAirTemp", temp) // Используем Set
		}
	case PID_TOTAL_DISTANCE:
		if len(paramData) >= 4 {
			distance := float64(
				int(paramData[0])<<24|
					int(paramData[1])<<16|
					int(paramData[2])<<8|
					int(paramData[3])) * 0.1 // км
			p.data.Set("TotalDistance", distance) // Используем Set
		}
	case PID_ACTIVE_DTC, PID_PREVIOUSLY_ACTIVE_DTC:
		if len(paramData) >= 3 { // Минимальная длина для одного DTC
			// Логика DTC остается прежней, так как DTC отправляются в канал, а не сохраняются в p.data
			dtcCodeRaw := int(paramData[0])
			fmiAndPidHigh := paramData[1]
			fmi := int(fmiAndPidHigh & 0x0F)

			dtc := common.DTCCode{
				Timestamp: time.Now().UnixNano(),
				MID:       mid,
				PID:       pid,        // Сохраняем PID, чтобы различать активные/предыдущие на стороне получателя, если нужно
				SPN:       dtcCodeRaw, // В J1587 это скорее PID-специфичный код ошибки, а не SPN
				FMI:       fmi,
			}

			// В common.DTCCode нет поля Active. Тип DTC (активный/предыдущий)
			// можно определить по PID_ACTIVE_DTC или PID_PREVIOUSLY_ACTIVE_DTC
			// или передавать это как отдельный параметр в MQTT, если необходимо.
			// Пока просто отправляем в общий канал dtcChan.
			select {
			case p.dtcChan <- dtc:
			default:
				log.Printf("Канал DTC переполнен, DTC (PID: %d) пропущен (J1587)", pid)
			}
		}

	default:
		// log.Printf("Неизвестный PID: %d для MID: %d", pid, mid)
	}
}

// processFrames обрабатывает полученные фреймы
func (p *Bus) processFrames() {
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
