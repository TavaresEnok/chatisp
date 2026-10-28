package httpapi

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/danielgtaylor/huma/v2"

	"chatisp/internal/platform/logging"
)

// Problem is an RFC 9457 body. It never carries a stack trace or the raw submitted value.
type Problem struct {
	Type   string       `json:"type" doc:"URI que identifica o tipo do problema." example:"urn:chatisp:problem:not-found"`
	Title  string       `json:"title" doc:"Resumo curto e estável do problema." example:"Recurso não encontrado"`
	Status int          `json:"status" doc:"Código HTTP." example:"404"`
	Detail string       `json:"detail,omitempty" doc:"Explicação deste caso, sem segredos nem stack trace." example:"o caminho solicitado não existe"`
	Errors []FieldError `json:"errors,omitempty" doc:"Erros por campo, sem o valor enviado."`
}

// FieldError describes one invalid input location.
type FieldError struct {
	Message  string `json:"message" doc:"Mensagem do erro de campo, sem o valor enviado."`
	Location string `json:"location,omitempty" doc:"Local do erro, como body.nome ou query.pagina."`
}

// Error returns the public detail.
func (p *Problem) Error() string {
	if p == nil || p.Detail == "" {
		return "erro"
	}
	return p.Detail
}

// GetStatus returns the HTTP status.
func (p *Problem) GetStatus() int {
	if p == nil {
		return http.StatusInternalServerError
	}
	return p.Status
}

// ContentType forces RFC 9457 problem JSON.
func (p *Problem) ContentType(ct string) string {
	if ct == "application/json" || ct == "application/problem+json" || ct == "" {
		return "application/problem+json"
	}
	return ct
}

func init() {
	huma.NewError = func(status int, msg string, errs ...error) huma.StatusError {
		return newProblem(status, msg, errs...)
	}
}

func newProblem(status int, msg string, errs ...error) *Problem {
	problem := &Problem{
		Type:   problemType(status),
		Title:  problemTitle(status),
		Status: status,
		Detail: problemDetail(status, msg),
	}
	if status >= 500 {
		return problem
	}
	for _, err := range errs {
		if err == nil {
			continue
		}
		var detailer huma.ErrorDetailer
		if ok := asDetailer(err, &detailer); ok {
			detail := detailer.ErrorDetail()
			if detail == nil {
				continue
			}
			problem.Errors = append(problem.Errors, FieldError{
				Message:  safeText(detail.Message),
				Location: safeLocation(detail.Location),
			})
			continue
		}
		problem.Errors = append(problem.Errors, FieldError{Message: safeText(err.Error())})
	}
	return problem
}

func asDetailer(err error, target *huma.ErrorDetailer) bool {
	detailer, ok := err.(huma.ErrorDetailer)
	if !ok || detailer == nil {
		return false
	}
	*target = detailer
	return true
}

// Write sends a problem+json response without a stack trace.
func Write(w http.ResponseWriter, status int, detail string) {
	problem := newProblem(status, detail)
	w.Header().Set("Content-Type", "application/problem+json")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	encoder := json.NewEncoder(w)
	encoder.SetEscapeHTML(false)
	_ = encoder.Encode(problem)
}

func problemType(status int) string {
	switch status {
	case http.StatusBadRequest:
		return "urn:chatisp:problem:bad-request"
	case http.StatusUnauthorized:
		return "urn:chatisp:problem:unauthorized"
	case http.StatusForbidden:
		return "urn:chatisp:problem:forbidden"
	case http.StatusNotFound:
		return "urn:chatisp:problem:not-found"
	case http.StatusMethodNotAllowed:
		return "urn:chatisp:problem:method-not-allowed"
	case http.StatusConflict:
		return "urn:chatisp:problem:conflict"
	case http.StatusUnprocessableEntity:
		return "urn:chatisp:problem:invalid"
	case http.StatusInternalServerError:
		return "urn:chatisp:problem:internal"
	case http.StatusServiceUnavailable:
		return "urn:chatisp:problem:unavailable"
	default:
		return "about:blank"
	}
}

func problemTitle(status int) string {
	switch status {
	case http.StatusBadRequest:
		return "Requisição inválida"
	case http.StatusUnauthorized:
		return "Não autenticado"
	case http.StatusForbidden:
		return "Acesso negado"
	case http.StatusNotFound:
		return "Recurso não encontrado"
	case http.StatusMethodNotAllowed:
		return "Método não permitido"
	case http.StatusConflict:
		return "Conflito"
	case http.StatusUnprocessableEntity:
		return "Dados inválidos"
	case http.StatusServiceUnavailable:
		return "Serviço indisponível"
	default:
		if status >= 500 {
			return "Erro interno"
		}
		return "Requisição inválida"
	}
}

func problemDetail(status int, msg string) string {
	if status >= 500 {
		return "ocorreu um erro interno"
	}
	switch strings.TrimSpace(msg) {
	case "":
		return defaultDetail(status)
	case "validation failed":
		return "a requisição é inválida"
	case "unexpected error occurred":
		return "ocorreu um erro interno"
	}
	if logging.Sensitive(msg) {
		return defaultDetail(status)
	}
	return msg
}

func defaultDetail(status int) string {
	switch status {
	case http.StatusNotFound:
		return "o caminho solicitado não existe"
	case http.StatusMethodNotAllowed:
		return "método não permitido para este caminho"
	case http.StatusServiceUnavailable:
		return "serviço indisponível"
	case http.StatusInternalServerError:
		return "ocorreu um erro interno"
	default:
		return "não foi possível concluir a requisição"
	}
}

func safeText(value string) string {
	if strings.TrimSpace(value) == "" || logging.Sensitive(value) {
		return "valor inválido"
	}
	return value
}

func safeLocation(value string) string {
	if value == "" || logging.Sensitive(value) || len(value) > 160 {
		return ""
	}
	return value
}
