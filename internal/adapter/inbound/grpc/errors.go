package grpc

import (
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"api/internal/adapter/inbound/grpc/gen/commonv1"
)

// Fault is an error the client is meant to see: gRPC code plus the ErrorDetail joseph-app decodes
type Fault struct {
	Status  codes.Code
	Code    string
	Message string
	Fields  map[string]string
}

var (
	errInternal    = Fault{Status: codes.Internal, Code: "internal", Message: "Ocurrió un error inesperado. Inténtalo de nuevo."}
	errRateLimited = Fault{Status: codes.ResourceExhausted, Code: "rate_limited", Message: "Demasiados intentos desde tu conexión. Espera un minuto."}
)

// toStatus carries the client-facing code, message and field errors as an ErrorDetail in the status details
func toStatus(f Fault) error {
	st := status.New(f.Status, f.Message)
	if withDetail, err := st.WithDetails(&commonv1.ErrorDetail{Code: f.Code, Message: f.Message, Fields: f.Fields}); err == nil {
		st = withDetail
	}
	return st.Err()
}
