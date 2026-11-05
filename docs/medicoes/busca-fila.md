# Medição da busca composta da fila (dados sintéticos)

Executável: `CHATISP_PERF=1 go test -count=1 -run TestQueueComposedSearchOnSyntheticVolume -v ./internal/attendance/store/`
(`backend/internal/attendance/store/queue_perf_test.go`, ~2 min, PostgreSQL 18 embutido). Fica fora do `go test ./...` por padrão.

## Dados

- 2 tenants, cada um com **20 000 conversas** (todas `closed`), **100 000 mensagens** públicas (5 por conversa), 2 000 contatos com nomes acentuados e telefone, 3 setores. O agente do teste pertence a 1 setor (6 666 candidatas); o admin vê as 20 000.
- Os dois tenants têm as mesmas palavras; o teste confere que nenhum item devolvido existe no outro tenant (isolamento por RLS + `tenant_id`).
- Distribuição das mensagens: `closed/public=100000` (nesta carga não há mensagens internas nem conversas abertas; o filtro de visibilidade é coberto pelo índice parcial e pelos testes de `store`).
- Máquina de desenvolvimento, sem outra carga relevante; cache de páginas aquecido pela própria carga.

## Consulta

A real: `queueSQL` em `queue.go` via `Queue()` (autorização por papel/setor, travas `FOR SHARE`, filtros, busca, ordem, cursor, contagens). Busca = nome (normalizado, LIKE), telefone, protocolo **ou** `EXISTS` em mensagens públicas com `to_tsvector('portuguese', m3_search_normalize(body)) @@ websearch_to_tsquery(...)`.

## Tempos (uma execução, página de 50, ms de parede incluindo travas e contagens)

| Cenário | Itens | Total filtrado | Tempo |
| --- | --- | --- | --- |
| agente, sem busca, ordem espera | 50 | 6 666 | 440 ms |
| admin, sem busca | 50 | 20 000 | 882 ms |
| agente, ordem última mensagem | 50 | 6 666 | 330 ms |
| agente, status + setor | 50 | 6 666 | 317 ms |
| busca por nome com acento (Conceição) | 50 | 1 122 | 665 ms |
| busca por nome sem acento (conceicao) | 50 | 1 122 | 812 ms |
| busca por telefone parcial | 4 | 4 | 808 ms |
| busca por protocolo | 36 | 36 | 964 ms |
| conteúdo raro (lentidão) | 1 | 1 | 841 ms |
| conteúdo raro sem acento (lentidao) | 1 | 1 | 849 ms |
| admin, conteúdo comum (instalação, ~1/6 das mensagens) | 50 | 19 991 | 2,24 s |
| agente, conteúdo + intervalo de datas + status | 50 | 959 | 161 ms |
| paginação: 12 páginas de 100 (sem repetição) | 1 200 | — | 361 ms/página |

## Plano de execução (agente, conteúdo "lentidão", 6 666 candidatas)

Execution Time ≈ 0,64–1,0 s. O plano **não usa** o índice GIN `m3_message_search` nesta consulta: a subconsulta `EXISTS` é executada por conversa candidata (`SubPlan` com `Index Scan using messages_timeline_idx`, `loops=6666`, 5 linhas removidas por filtro em cada). O GIN só entra quando a consulta é isolada (50 000 mensagens, 0,06 ms, `internal/searchplan`). O custo é, portanto, **linear no número de conversas autorizadas × mensagens por conversa**, e não no número de acertos.

## Tentativa descartada

Trocar o `EXISTS` por `c.id IN (SELECT conversation_id FROM messages WHERE ... @@ ...)` (subplan não correlacionado, para o GIN ser usado uma vez) ficou **mais lento** (≈1,5–1,9 s nas buscas por nome/telefone e 2,5 s no conteúdo comum), porque o `OR` força o cálculo do subplan em toda consulta de busca. Mantida a consulta original.

## Leitura honesta

- Com 20 000 conversas por tenant e 100 000 mensagens, toda página fica abaixo de ~1 s para o agente e ~2,3 s para o admin no pior caso medido. Isto vale para este volume sintético, nesta máquina; **não é promessa de capacidade** (D2-07 continua pendente).
- A especificação pede "índices e planos medidos, sem promessa de escala não testada": o plano está registrado, a limitação (varredura por candidata) também.
- Se o piloto real passar de algumas dezenas de milhares de conversas por tenant, o caminho já identificado é separar a busca de conteúdo em consulta própria (GIN → conjunto de conversas → interseção com a autorização), em vez de `OR` na mesma condição. Não foi feito porque a tentativa direta piorou e não há volume real que o justifique.
