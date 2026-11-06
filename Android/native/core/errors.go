package bridge

import "errors"

var errConfig = errors.New("INVALID_CONFIG")
var errNetwork = errors.New("NETWORK_UNAVAILABLE")
var errClosed = errors.New("CORE_CLOSED")
var errStart = errors.New("CORE_START_FAILED")
