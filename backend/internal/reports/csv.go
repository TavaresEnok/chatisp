package reports

import (
	"strconv"
	"strings"
)

func renderCSV(report Report) string {
	var b strings.Builder
	b.WriteString("secao,chave,valor\n")
	for i, line := range report.Definitions {
		b.WriteString("definicao," + strconv.Itoa(i+1) + "," + cell(line) + "\n")
	}
	writePair(&b, "resumo", "fuso", report.Timezone)
	writePair(&b, "resumo", "inicio", report.From.UTC().Format("2006-01-02T15:04:05Z"))
	writePair(&b, "resumo", "fim", report.To.UTC().Format("2006-01-02T15:04:05Z"))
	writePair(&b, "resumo", "agrupamento", report.Group)
	writePair(&b, "resumo", "conversas", strconv.Itoa(report.Conversations))
	writePair(&b, "resumo", "ciclos", strconv.Itoa(report.Cycles))
	writePair(&b, "resumo", "mensagens", strconv.Itoa(report.Messages))
	b.WriteString("secao,metrica,amostra,media_segundos,mediana_segundos,nao_aplicavel\n")
	writeStats(&b, "primeira_resposta_humana", report.FirstResponse)
	writeStats(&b, "encerramento_total", report.CloseTotal)
	writeStats(&b, "encerramento_operacional", report.CloseOperational)
	writeStats(&b, "espera_fila", report.QueueWait)
	b.WriteString("secao,resultado,autoria,rotulo,quantidade\n")
	for _, row := range report.Results {
		b.WriteString("resultado," + cell(row.Result) + "," + cell(row.AuthorKind) + "," + cell(row.Label) + "," + strconv.Itoa(row.Count) + "\n")
	}
	b.WriteString("secao,nome,ciclos,esperas,mediana_espera_segundos,responsabilidade_segundos\n")
	for _, row := range report.Sectors {
		b.WriteString("setor," + cell(row.Name) + "," + strconv.Itoa(row.Cycles) + "," + strconv.Itoa(row.QueueWaits) + "," + seconds(row.QueueWaitMedianSeconds) + "," + strconv.FormatInt(row.ResponsibilitySeconds, 10) + "\n")
	}
	b.WriteString("secao,atendente,responsabilidade_segundos,mensagens,encerramentos\n")
	for _, row := range report.Attendants {
		b.WriteString("atendente," + cell(row.Label) + "," + strconv.FormatInt(row.ResponsibilitySeconds, 10) + "," + strconv.Itoa(row.Messages) + "," + strconv.Itoa(row.Closures) + "\n")
	}
	b.WriteString("secao,caixa,ciclos,mensagens\n")
	for _, row := range report.Inboxes {
		b.WriteString("caixa," + cell(row.Name) + "," + strconv.Itoa(row.Cycles) + "," + strconv.Itoa(row.Messages) + "\n")
	}
	b.WriteString("secao,motivo,quantidade,sobreposicao\n")
	for _, row := range report.Reasons {
		b.WriteString("motivo," + cell(row.Name) + "," + strconv.Itoa(row.Count) + ",nao\n")
	}
	for _, row := range report.Tags {
		b.WriteString("etiqueta," + cell(row.Name) + "," + strconv.Itoa(row.Count) + ",sim\n")
	}
	b.WriteString("secao,dia_semana,hora,ciclos\n")
	for _, row := range report.Heatmap {
		b.WriteString("calor," + weekdayName(row.Weekday) + "," + strconv.Itoa(row.Hour) + "," + strconv.Itoa(row.Cycles) + "\n")
	}
	b.WriteString("secao,chave,ciclos,mensagens,encerrados,amostra_primeira_resposta,media_primeira_resposta,amostra_operacional,media_operacional\n")
	for _, row := range report.Buckets {
		b.WriteString("agrupamento," + cell(row.Key) + "," + strconv.Itoa(row.Cycles) + "," + strconv.Itoa(row.Messages) + "," + strconv.Itoa(row.Closed) + "," + strconv.Itoa(row.FirstResponseSample) + "," + seconds(row.FirstResponseAverage) + "," + strconv.Itoa(row.OperationalSample) + "," + seconds(row.OperationalAverage) + "\n")
	}
	b.WriteString("secao,chave,valor\n")
	writePair(&b, "agora", "na_fila", strconv.Itoa(report.Live.InQueue))
	writePair(&b, "agora", "em_atendimento", strconv.Itoa(report.Live.InAttendance))
	writePair(&b, "agora", "aguardando_cliente", strconv.Itoa(report.Live.WaitingCustomer))
	writePair(&b, "agora", "adiadas", strconv.Itoa(report.Live.Snoozed))
	writePair(&b, "agora", "sem_responsavel", strconv.Itoa(report.Live.Unassigned))
	writePair(&b, "agora", "atendentes_online", strconv.Itoa(report.Live.OnlineAttendants))
	writePair(&b, "agora", "maior_espera_segundos", strconv.FormatInt(report.Live.LongestQueueWaitSeconds, 10))
	writePair(&b, "agora", "alertas_espera", strconv.Itoa(report.Live.WaitAlerts))
	writePair(&b, "agora", "limite_espera_segundos", strconv.Itoa(report.Live.WaitAlertSeconds))
	return b.String()
}

func writePair(b *strings.Builder, section, key, value string) {
	b.WriteString(section + "," + cell(key) + "," + cell(value) + "\n")
}

func writeStats(b *strings.Builder, metric string, stats Stats) {
	b.WriteString("tempos," + metric + "," + strconv.Itoa(stats.Sample) + "," + seconds(stats.AverageSeconds) + "," + seconds(stats.MedianSeconds) + "," + strconv.Itoa(stats.NotApplicable) + "\n")
}

func seconds(value *int64) string {
	if value == nil {
		return ""
	}
	return strconv.FormatInt(*value, 10)
}

func cell(value string) string {
	if strings.ContainsAny(value, ",\"\n\r") {
		return `"` + strings.ReplaceAll(value, `"`, `""`) + `"`
	}
	return value
}

func weekdayName(day int) string {
	names := []string{"domingo", "segunda", "terça", "quarta", "quinta", "sexta", "sábado"}
	if day < 0 || day >= len(names) {
		return "dia"
	}
	return names[day]
}
