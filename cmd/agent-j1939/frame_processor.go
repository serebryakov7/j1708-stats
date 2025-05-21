// go:build linux
//go:build linux
// +build linux

package main

import (
	"encoding/binary"
	"log"
	"time"

	"github.com/serebryakov7/j1708-stats/common"
	"github.com/serebryakov7/j1708-stats/pkg/storage" // Добавлено для использования bbolt
	bolt "go.etcd.io/bbolt"                           // Добавлено для типа *bolt.DB
)

// Временные PGN значения, так как константы из can.PGN_* не найдены
const (
	pgnEEC1 uint32 = 0xF004 // Electronic Engine Controller 1 (SPN 513 - Actual Engine % Torque, SPN 190 - Engine Speed)
	pgnEEC2 uint32 = 0xF003 // Electronic Engine Controller 2 (SPN 91 - Accelerator Pedal Position 1)
	pgnLFE  uint32 = 0xFEF2 // Fuel Economy (Liquid) (SPN 184 - Engine Instantaneous Fuel Economy)
	pgnGPS  uint32 = 0xFEF1 // Vehicle Position (Latitude/Longitude) - Это пример, PGN для GPS может быть разным (e.g., 65267 / 0xFEF1 - Vehicle Position)
	pgnVDHR uint32 = 0xFEE4 // High Resolution Vehicle Distance (SPN 245 - Total Vehicle Distance)
	pgnCI   uint32 = 0xFEF7 // Component Identification (SPN 237 - VIN) - часто требует TP
	pgnET1  uint32 = 0xFEEF // Engine Temperature 1 (SPN 110 - Engine Coolant Temperature)
	pgnEP1  uint32 = 0xFEEB // Engine Pressure 1 (SPN 100 - Engine Oil Pressure)
	pgnFL   uint32 = 0xFEFC // Fuel Level (SPN 96 - Fuel Level 1)
	pgnVI   uint32 = 0xFEEC // Vehicle Identification (VIN) - часто требует TP
	pgnAmb  uint32 = 0xFEF5 // Ambient Conditions (SPN 171 - Ambient Air Temperature)
	pgnDM1  uint32 = 0xFECA // DM1 (Active Diagnostic Trouble Codes)
	pgnDM2  uint32 = 0xFECB // DM2 (Previously Active Diagnostic Trouble Codes)
)

type FrameProcessor struct {
	data    *J1939Data // Указатель на структуру для хранения данных J1939 (теперь ProtectedData)
	dtcChan chan common.DTCCode
	db      *bolt.DB // Добавлено для bbolt
}

// NewFrameProcessor создает новый экземпляр FrameProcessor.
// db передается из main.go после инициализации.
func NewFrameProcessor(data *J1939Data, dtcChan chan common.DTCCode, db *bolt.DB) *FrameProcessor {
	return &FrameProcessor{
		data:    data,
		dtcChan: dtcChan,
		db:      db, // Сохраняем ссылку на базу данных
	}
}

// ProcessFrame разбирает фрейм J1939 и обновляет J1939Data.
// Ранее этот метод назывался parseFrame.
func (fp *FrameProcessor) ProcessFrame(pgn uint32, sa uint8, data []byte) {
	// Блокировка мьютекса теперь внутри методов Set/Get J1939Data (ProtectedData)
	// Сохраняем копию сырых данных кадра в специальное поле в карте, если это необходимо.
	// Для этого можно использовать ключ, например, "raw_pgn_XXXX"
	// rawDataCopy := make([]byte, len(data))
	// copy(rawDataCopy, data)
	// fp.data.Set(fmt.Sprintf("raw_pgn_%X", pgn), rawDataCopy)

	switch pgn {
	case pgnEEC1:
		fp.parseEEC1(data)
	case pgnGPS:
		fp.parseVehiclePosition(data)
	case pgnLFE:
		fp.parseFuelConsumption(data)
	case pgnAmb:
		fp.parseAmbientConditions(data)
	case pgnDM1:
		fp.parseDM1(data, sa)
	case pgnDM2:
		fp.parseDM2(data, sa)
	default:
		// log.Printf("FrameProcessor: Неизвестный или необрабатываемый PGN: 0x%X от SA: 0x%X", pgn, sa)
	}
}

// parseEEC1 парсит данные от электронного блока управления двигателем (PGN F004)
func (fp *FrameProcessor) parseEEC1(data []byte) {
	if len(data) < 5 { // Обычно 8 байт, но проверяем хотя бы на 5 для оборотов
		return
	}
	// SPN 190: Engine Speed (Bytes 4, 5)
	// Resolution: 0.125 rpm/bit, Offset: 0
	if data[3] != 0xFF || data[4] != 0xFF { // Проверка на "not available"
		rpmRaw := uint16(data[3]) | (uint16(data[4]) << 8)
		rpm := float64(rpmRaw) * 0.125
		fp.data.Set("EngineRPM", rpm)
	} else {
		fp.data.Set("EngineRPM", nil) // Используем Set для установки значения
	}

	// SPN 513: Actual Engine - Percent Torque (Byte 3)
	// Resolution: 1 %/bit, Offset: -125 %
	if data[2] != 0xFF { // Проверка на "not available"
		// Значение data[2] это unsigned int (0-255). Offset -125. Диапазон -125% до 125%.
		// 0 -> -125%, 125 -> 0%, 250 -> 125%
		load := float64(data[2]) - 125.0
		fp.data.Set("EngineLoad", load)
	} else {
		fp.data.Set("EngineLoad", nil)
	}
}

func (fp *FrameProcessor) parseVehiclePosition(data []byte) {
	if len(data) < 8 {
		return
	}
	// SPN 584: Latitude (Bytes 1-4)
	// Resolution: 1e-7 deg/bit, Offset: -210 deg
	if !(data[0] == 0xFF && data[1] == 0xFF && data[2] == 0xFF && data[3] == 0xFF) {
		latRaw := int32(binary.LittleEndian.Uint32(data[0:4]))
		lat := (float64(latRaw) * 1e-7) // Смещение -210 градусов уже учтено в знаковом int32, если данные закодированы так.
		// Стандарт J1939-71 говорит: "Data Range: –210 to +210 deg".
		// Если latRaw это просто биты, то смещение нужно применять.
		// Обычно, если тип int32, то смещение уже учтено.
		fp.data.Set("Latitude", lat)
	} else {
		fp.data.Set("Latitude", nil)
	}
	// SPN 585: Longitude (Bytes 5-8)
	// Resolution: 1e-7 deg/bit, Offset: -210 deg
	if !(data[4] == 0xFF && data[5] == 0xFF && data[6] == 0xFF && data[7] == 0xFF) {
		lonRaw := int32(binary.LittleEndian.Uint32(data[4:8]))
		lon := (float64(lonRaw) * 1e-7)
		fp.data.Set("Longitude", lon)
	} else {
		fp.data.Set("Longitude", nil)
	}
}

func (fp *FrameProcessor) parseFuelConsumption(data []byte) { // Это может быть LFE (PGN FEF2)
	if len(data) < 2 { // Для SPN 183 (Engine Fuel Rate) достаточно 2 байта
		return
	}
	// SPN 183: Engine Fuel Rate (Bytes 1-2 in LFE)
	// Resolution: 0.05 L/h per bit, Offset: 0
	if data[0] != 0xFF || data[1] != 0xFF { // Проверка на "not available" (0xFFFF)
		fuelRateRaw := binary.LittleEndian.Uint16(data[0:2]) // J1939 обычно Little Endian для многобайтовых SPN
		fuelRate := float64(fuelRateRaw) * 0.05              // L/h
		fp.data.Set("FuelConsumption", fuelRate)
	} else {
		fp.data.Set("FuelConsumption", nil)
	}
}

func (fp *FrameProcessor) parseAmbientConditions(data []byte) {
	if len(data) < 2 { // Для SPN 171 (Ambient Air Temperature) (байты 1-2)
		return
	}
	// SPN 171: Ambient Air Temperature (Bytes 1-2)
	// Resolution: 0.03125 C/bit, Offset: -273 C
	// Значение 0xFFFF означает "not available"
	if data[0] == 0xFF && data[1] == 0xFF {
		fp.data.Set("AmbientAirTemp", nil)
		return
	}
	// Удалена неиспользуемая переменная tempRawSigned
	tempRawUnsigned := binary.LittleEndian.Uint16(data[0:2])
	temp := (float64(tempRawUnsigned) * 0.03125) - 273.0
	fp.data.Set("AmbientAirTemp", temp)
}

func (fp *FrameProcessor) parseDM1(data []byte, sa uint8) {
	if len(data) < 6 { // Минимальный пакет с одним DTC: 2 (LS) + 4 (DTC) = 6 байт.
		// Если len(data) < 6, то это только Lamp Status или неполный DTC.
		// В этом случае не пытаемся парсить DTC.
		// Ранее здесь была логика очистки fp.data.ActiveDTCCodes,
		// но теперь DTC не хранятся в fp.data, а отправляются в канал.
		// Поэтому, если нет полных DTC, просто выходим.
		return
	}

	// Первые 2 байта - Lamp Status, пропускаем их для извлечения DTC
	// DTC передаются группами по 4 байта, начиная с индекса 2
	// data[0], data[1] - Lamp Status (MIL, RSL, AWL, PL)
	// data[2] - SPN LSB
	// data[3] - SPN MSB
	// data[4] - FMI (5 бит) + SPN HSB (3 бита)
	// data[5] - OC (7 бит) + CM (1 бит)

	numDTCs := (len(data) - 2) / 4
	if (len(data)-2)%4 != 0 {
		log.Printf("FrameProcessor: parseDM1: длина данных DM1 (%d байт) некорректна для SA %d, ожидается 2 + N*4 байт", len(data), sa)
		// Можно решить не обрабатывать такой пакет или обработать только полные DTC
		numDTCs = (len(data) - 2) / 4 // Целочисленное деление даст количество полных DTC
	}

	for i := 0; i < numDTCs; i++ {
		offset := 2 + i*4
		if offset+3 >= len(data) { // Убедимся, что не выходим за пределы среза
			break
		}

		spnLow := uint16(data[offset])
		spnMid := uint16(data[offset+1])
		spnHighBits := uint8(data[offset+2] >> 5) // 3 старших бита SPN из байта SPN_MSB_FMI

		spn := uint32(spnLow) | (uint32(spnMid) << 8) | (uint32(spnHighBits) << 16)
		fmi := uint8(data[offset+2] & 0x1F) // 5 младших бит FMI из байта SPN_MSB_FMI
		// cm := (data[offset+3] & 0x80) >> 7 // Conversion Method, 0 = J1939-73 Mode 1
		oc := data[offset+3] & 0x7F // Occurrence Count

		// Проверяем, новый ли это DTC, перед отправкой в канал
		if fp.db != nil { // Убедимся, что база данных инициализирована
			isNew, err := storage.IsNew(fp.db, spn, fmi)
			if err != nil {
				log.Printf("FrameProcessor: parseDM1: ошибка проверки DTC в bbolt для SA %d: SPN=%d, FMI=%d: %v", sa, spn, fmi, err)
				// Решаем, отправлять ли DTC, если проверка bbolt не удалась.
				// В данном случае, отправим, чтобы не потерять информацию.
			} else if !isNew {
				// log.Printf("FrameProcessor: parseDM1: DTC SPN=%d, FMI=%d от SA %d уже зарегистрирован, пропускаем.", spn, fmi, sa)
				continue // DTC не новый, пропускаем
			}
			// Если isNew is true, DTC новый, продолжаем и отправляем
		} else {
			log.Println("FrameProcessor: parseDM1: bbolt DB не инициализирована, DTC не проверяются на уникальность.")
			// Если БД нет, отправляем все DTC
		}

		dtc := common.DTCCode{
			MID:       int(sa), // Используем Source Address как MID
			SPN:       int(spn),
			FMI:       int(fmi),
			OC:        int(oc),
			Timestamp: time.Now().UnixNano(), // Используем UnixNano() для int64
		}
		// log.Printf("FrameProcessor: parseDM1: Обнаружен активный DTC от SA %d: SPN=%d, FMI=%d, OC=%d", sa, spn, fmi, oc)
		// Признак активности (DM1) подразумевается, отдельное поле Active в common.DTCCode не используется в этом варианте.
		fp.dtcChan <- dtc
	}
}

func (fp *FrameProcessor) parseDM2(data []byte, sa uint8) {
	if len(data) < 6 { // Аналогично DM1, минимум 6 байт для одного DTC
		return
	}

	numDTCs := (len(data) - 2) / 4
	if (len(data)-2)%4 != 0 {
		log.Printf("FrameProcessor: parseDM2: длина данных DM2 (%d байт) некорректна для SA %d, ожидается 2 + N*4 байт", len(data), sa)
		numDTCs = (len(data) - 2) / 4
	}

	for i := 0; i < numDTCs; i++ {
		offset := 2 + i*4
		if offset+3 >= len(data) {
			break
		}

		spnLow := uint16(data[offset])
		spnMid := uint16(data[offset+1])
		spnHighBits := uint8(data[offset+2] >> 5)
		spn := uint32(spnLow) | (uint32(spnMid) << 8) | (uint32(spnHighBits) << 16)
		fmi := uint8(data[offset+2] & 0x1F)
		oc := data[offset+3] & 0x7F

		dtc := common.DTCCode{
			MID:       int(sa), // Используем Source Address как MID
			SPN:       int(spn),
			FMI:       int(fmi),
			OC:        int(oc),
			Timestamp: time.Now().UnixNano(), // Используем UnixNano() для int64
		}
		// log.Printf("FrameProcessor: parseDM2: Обнаружен ранее активный DTC от SA %d: SPN=%d, FMI=%d, OC=%d", sa, spn, fmi, oc)
		// Признак неактивности (DM2) подразумевается, отдельное поле Active в common.DTCCode не используется.
		// Если необходимо различать DM1 и DM2 на уровне получателя, можно добавить отдельное поле в MQTT сообщение
		// или использовать разные топики.
		fp.dtcChan <- dtc
	}
}

// Другие неиспользуемые функции, такие как HandleFrame и GetData, которые были основаны на ConfigSnapshotParam, удалены.
// Если они нужны для другой функциональности, их следует восстановить и адаптировать.
