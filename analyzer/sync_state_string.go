package analyzer

func (s SyncState) String() string {
	switch s {
	case StateIdle:
		return "idle"
	case StateComputing:
		return "computing"
	case StateWriting:
		return "writing"
	case StateError:
		return "error"
	case StatePaused:
		return "paused"
	default:
		return "unknown"
	}
}
