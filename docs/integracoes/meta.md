# Meta Cloud API

A D2-03 continua sem app, WABA, número, permissão, credencial e sem versão da Graph API. Este arquivo não escolhe uma versão.

O driver local está em `backend/internal/channel/whatsapp/metacloud`. O contrato de texto e do decoder está em `docs/contracts/m2-meta.md`. A configuração manual está em `docs/contracts/m2-channel-configuration.md`. O servidor de teste é um stand-in e responde com `X-ChatISP-Simulation: meta-cloud`.

O que o código já cobre, só em simulação: token, App ID, App Secret, WABA ID e Phone Number ID na configuração; verificação de webhook com token próprio; HMAC do corpo bruto; recusa de assinatura inválida; `biz_opaque_callback_data` como correlação, sem tratar isso como idempotência; templates da WABA no catálogo local; janela de 24 horas só no conector oficial.

Não houve chamada à Graph API nem homologação com a Meta.
