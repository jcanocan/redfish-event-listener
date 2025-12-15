package redfish

import "regexp"

// These are the message IDs that indicate a watchdog
// reset event by the corresponding vendor:
// "ASR0001" 				Dell
// "IPMIWatchdogTimerReset" HPE
// "0xc804ff"               SuperMicro
// "0x806f0823210104ff"     Lenovo
var watchdogResetMessageIDRe = regexp.MustCompile(`ASR0001|IPMIWatchdogTimerReset|0xc804ff|0x806f0823210104ff`)

func IsWatchdogResetEvent(messageID string) bool {
	return watchdogResetMessageIDRe.MatchString(messageID)
}
