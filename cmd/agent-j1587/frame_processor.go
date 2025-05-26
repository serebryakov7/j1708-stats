package main

import (
	"fmt"
	"log"
	"time"

	"github.com/serebryakov7/j1708-stats/common"
)

// calculateJ1587Checksum вычисляет контрольную сумму для J1587 фрейма
func calculateJ1587Checksum(frame []byte) byte {
	sum := 0
	for _, b := range frame {
		sum += int(b)
	}
	return byte(256 - (sum % 256))
}

// validateJ1587Checksum проверяет контрольную сумму J1587 фрейма
func validateJ1587Checksum(frame []byte) bool {
	if len(frame) < 3 { // MID + минимум 1 PID + checksum
		return false
	}

	sum := 0
	for _, b := range frame {
		sum += int(b)
	}
	return (sum % 256) == 0
}

// getPIDDataLength возвращает длину данных для заданного PID согласно SAE J1587
func getPIDDataLength(pid byte, data []byte, offset int) (int, error) {
	switch {
	case pid >= 0 && pid <= 127:
		// PID 0-127: 1 байт данных
		return 1, nil
	case pid >= 128 && pid <= 191:
		// PID 128-191: 2 байта данных
		return 2, nil
	case pid >= 192 && pid <= 253:
		// PID 192-253: переменная длина, следующий байт указывает количество байт данных
		if offset >= len(data) {
			return 0, fmt.Errorf("недостаточно данных для чтения длины переменного PID %d", pid)
		}
		dataLength := int(data[offset])
		return dataLength, nil
	default:
		return 0, fmt.Errorf("недопустимый PID: %d", pid)
	}
}

// parseFrame разбирает фрейм J1587 с поддержкой нескольких PID/Data блоков
func (p *Bus) parseFrame(frame []byte) {
	if len(frame) < 3 { // MID + минимум 1 PID + 1 байт данных или checksum
		log.Printf("J1587: фрейм слишком короткий: %d байт", len(frame))
		return
	}

	// Проверяем контрольную сумму
	if !validateJ1587Checksum(frame) {
		log.Printf("J1587: неверная контрольная сумма для фрейма: % X", frame)
		return
	}

	mid := int(frame[0])
	data := frame[1 : len(frame)-1] // Исключаем последний байт (checksum)

	log.Printf("J1587: парсинг фрейма MID=%d, данные=% X", mid, data)

	// Парсим все PID/Data блоки в фрейме
	offset := 0
	for offset < len(data) {
		if offset >= len(data) {
			break
		}

		pid := data[offset]
		offset++

		// Определяем длину данных для этого PID
		dataLength, err := getPIDDataLength(pid, data, offset)
		if err != nil {
			log.Printf("J1587: ошибка определения длины данных для PID %d: %v", pid, err)
			break
		}

		// Для переменной длины (PID 192-253) нужно прочитать байт длины
		if pid >= 192 && pid <= 253 {
			if offset >= len(data) {
				log.Printf("J1587: недостаточно данных для чтения длины PID %d", pid)
				break
			}
			dataLength = int(data[offset])
			offset++
		}

		// Проверяем, что у нас достаточно данных
		if offset+dataLength > len(data) {
			log.Printf("J1587: недостаточно данных для PID %d: нужно %d байт, доступно %d",
				pid, dataLength, len(data)-offset)
			break
		}

		// Извлекаем данные для этого PID
		paramData := data[offset : offset+dataLength]
		offset += dataLength

		log.Printf("J1587: обработка PID=%d, данные=% X", pid, paramData)

		// Обрабатываем конкретный PID
		p.processPIDData(mid, int(pid), paramData)
	}
}

// processPIDData обрабатывает данные для конкретного PID
func (p *Bus) processPIDData(mid int, pid int, paramData []byte) {
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
		log.Printf("J1587: неизвестный PID: %d для MID: %d", pid, mid)
	}
}

// processFrames обрабатывает полученные фреймы
func (p *Bus) processFrames() {
	for {
		select {
		case <-p.stopChan:
			return
		case frame := <-p.frames:
			if len(frame) < 3 { // MID + минимум 1 PID + checksum
				log.Printf("J1587: получен слишком короткий фрейм: %d байт", len(frame))
				continue
			}

			// Выводим фрейм для отладки
			log.Printf("J1587 FRAME: % X", frame)

			// Парсим фрейм J1587
			p.parseFrame(frame)
		}
	}
}
