package protocol

// Envelope is the stable output and session response format.
type Envelope struct {
	OK    bool   `json:"ok"`
	Data  any    `json:"data,omitempty"`
	Error *Error `json:"error,omitempty"`
}

type Error struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func Success(data any) Envelope { return Envelope{OK: true, Data: data} }
func Failure(code, message string) Envelope {
	return Envelope{OK: false, Error: &Error{Code: code, Message: message}}
}

type SessionRequest struct {
	Command   string `json:"command"`
	Reference string `json:"reference,omitempty"`
}
