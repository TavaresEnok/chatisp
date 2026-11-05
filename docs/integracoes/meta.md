# Meta Cloud API

A D2-03 continua sem app, WABA, número, permissão, credencial e sem versão da Graph API. Este arquivo não escolhe uma versão.

O driver local está em `backend/internal/channel/whatsapp/metacloud`. O contrato de texto e do decoder está em `docs/contracts/m2-meta.md`. A configuração manual está em `docs/contracts/m2-channel-configuration.md`. O servidor de teste é um stand-in e responde com `X-ChatISP-Simulation: meta-cloud`.

O que o código já cobre, só em simulação: token, App ID, App Secret, WABA ID e Phone Number ID na configuração; verificação de webhook com token próprio; HMAC do corpo bruto; recusa de assinatura inválida; `biz_opaque_callback_data` como correlação, sem tratar isso como idempotência; templates da WABA no catálogo local; janela de 24 horas só no conector oficial.

Não houve chamada à Graph API nem homologação com a Meta.

## Contrato oficial verificado em 05/11/2026 (leitura da documentação pública, sem chamada à API)

Páginas lidas: `developers.facebook.com/docs/whatsapp/cloud-api/webhooks/reference/phone_number_quality_update`, `.../webhooks/reference/messages/status` e `.../reference/phone-numbers`. Isto **não fixa a versão da Graph API** (as páginas mostram exemplos v15.0 e v27.0): a D2-03 continua aberta.

- Status de mensagem, objeto `pricing`: `billable` (booleano, a Meta avisa que será descontinuado), `pricing_model` (`CBP` ou `PMP`), `type` (`regular`, `free_customer_service`, `free_entry_point`, `free_group_customer_service`), `category` (`authentication`, `authentication-international`, `group_marketing`, `group_service`, `group_utility`, `marketing`, `marketing_lite`, `referral_conversion`, `service`, `utility`). O chat já decodifica categoria, tipo, modelo e indicador e agrega por categoria em Relatórios ("Uso reportado pela Meta"), sem valor em dinheiro.
- Webhook `phone_number_quality_update`: `value` traz `display_phone_number`, `event` (`ONBOARDING`, `THROUGHPUT_UPGRADE`), `current_limit` e `old_limit` (`TIER_50`, `TIER_250`, `TIER_2K`, `TIER_10K`, `TIER_100K`, `TIER_NOT_SET`, `TIER_UNLIMITED`; a Meta anuncia a remoção em fev/2026) e `max_daily_conversations_per_business`. **Não há campo de nota de qualidade** nesse webhook; ele não traz `phone_number_id`, só o WABA (`entry.id`) e o número de exibição.
- Leitura por Graph (`GET /<API_VERSION>/<PHONE_NUMBER_ID>?fields=...`): `quality_rating` aparece com `GREEN`, `NA`, `UNKNOWN` em exemplos, sem lista completa; `messaging_limit_tier` não aparece nessa página. Exige token e versão (D2-03).

## Saúde e limites do número (02-saude-numero, 02-limites-numero), implementados em 05/11/2026

- **Webhook de limite**: `phone_number_quality_update` é decodificado em `metacloud/decode_number.go` como item `number_limit`, com os valores como a Meta envia (evento, `current_limit`, `old_limit`, `max_daily_conversations_per_business`). Nenhum nível é traduzido para uma contagem. Formato fora do documentado vai para quarentena `limite_invalido`.
- **Roteamento** (decisão local, sem inventar campo): o webhook não traz `phone_number_id`. O ingresso liga o evento ao número quando o App ID confiável, o WABA (`entry.id`) e os dígitos do número de exibição batem com **exatamente um** número ativo. Nenhum número ou mais de um vão para `ingress_quarantine` (`unknown_display_number` ou `ambiguous_display_number`), sem palpite. Por isso o campo "Número exibido" da caixa precisa estar preenchido em E.164. Tabela `meta_number_health` (migração `00049`, RLS por tenant; o ingresso só insere linha de limite).
- **Nota de qualidade**: leitura sob demanda do administrador, `POST /api/panel/channels/meta/{connector_id}/quality`, que faz `GET /<versão>/<phone_number_id>?fields=quality_rating` com o token do canal (`metacloud/quality.go`). O valor é guardado como veio (`GREEN`, `NA`, `UNKNOWN` nos exemplos; sem lista fechada). Erro 190 degrada o canal como no envio. Só o servidor simulado recebe essa leitura hoje: o despacho oficial continua recusado na partida e a versão da Graph não tem padrão (D2-03). Sem leitor configurado, a rota responde 503.
- **Painel**: `GET /api/panel/channels/meta/health` e a seção "Saúde e limites dos números" em Configurações → Caixas Meta mostram último limite, última nota (marcada quando veio do servidor simulado), envios com falha por código e incertos em 7 dias, itens em quarentena, última mensagem recebida e alerta de credencial.
- **Correção junto**: o modo oficial do `DriverFactory` fixava `v23.0` e uma URL de documentação. Agora `GraphVersion` e `PrimaryDocURL` vêm da configuração e não têm padrão; sem eles a ativação oficial falha antes de qualquer pedido. `v23.0` ficou só como caminho do servidor simulado.

Homologação pendente: receber um `phone_number_quality_update` real e ler `quality_rating` com app, WABA, token e versão da Graph definidos (D2-03).
