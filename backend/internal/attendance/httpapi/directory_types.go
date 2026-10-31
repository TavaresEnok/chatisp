package httpapi

import (
	"chatisp/internal/attendance/service"
)

type directoryInput struct {
	InboxInput
	After string `query:"after" doc:"UUID do último vínculo da página anterior"`
	Limit int    `query:"limit" minimum:"0" maximum:"100" doc:"Itens por página (padrão 50, máximo 100)"`
}
type directoryOutput struct{ Body service.OperatorDirectoryPage }
type transferSectorsOutput struct{ Body []service.TransferSector }
