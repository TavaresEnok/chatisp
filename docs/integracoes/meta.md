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

Consequência para os requisitos 02-saude-numero e 02-limites-numero: o webhook de limite está documentado, mas hoje cai em quarentena `campo_nao_suportado` e não há como ligá-lo a um número sem decidir o roteamento por WABA + número de exibição; a nota de qualidade só vem da leitura por Graph, que depende de credencial e versão. Nada disso foi inventado.
