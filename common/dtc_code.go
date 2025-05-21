package common

// DTCCode представляет код неисправности (DTC)
type DTCCode struct {
	MID       int   `json:"mid"`           // Message Identifier (J1587) или Source Address (J1939)
	PID       int   `json:"pid,omitempty"` // Parameter Identifier (J1587)
	SPN       int   `json:"spn,omitempty"` // Suspect Parameter Number (J1939)
	FMI       int   `json:"fmi"`           // Failure Mode Identifier
	OC        int   `json:"oc,omitempty"`  // Occurrence Count
	Timestamp int64 `json:"timestamp"`     // Время обнаружения (Unix Nano)
}
