# Entrega M4B-M6 — aceite integrado do recorte

Data: 04/11/2026. Este recorte não conclui M4 nem M6 e não homologa produção. Não houve deploy, Meta, ERP, Evolution nem API paga.

## O que a prova executou

Chromium `140.0.7339.186` pelo launcher `/opt/paseo-browser/chromium-launcher`. `npm --prefix web run test:e2e:m4b-m6` terminou com código 0. A API descartável escutou em `127.0.0.1:44703` (`CHATISP_E2E_API_PORT`). A evidência está em `.paseo-build-tmp/m4b-m6-browser/m4b-m6-jornada.json`.

A fixture é `m2demo m4b`: o mesmo runtime de M3B/M4A, com fetcher simulado da imagem, ambiente `development`, anel de chaves e worker de webhook de saída. O ERP fake fica ligado ao setor da conversa, com concessão de faturas para o primeiro atendente. O stand-in Meta continua local. O receptor do webhook é um HTTP em `127.0.0.1` criado pelo teste.

- O admin cadastrou a assinatura `message.created` para `http://127.0.0.1:<porta>/m4b-hook` e leu o segredo uma vez.
- O stand-in entregou a imagem com legenda "Fachada simulada" e a localização "Sede simulada", endereço "Rua da Simulação, 1", coordenadas `-23.55, -46.63`. O painel mostrou "Imagem" e "Localização".
- O atendente assumiu a conversa, respondeu à localização e enviou `boleto.pdf` (`%PDF-1.7`). O compositor mostrou "Mídia aceita pelo conector". O cartão "Documento de Atendimento" citou "Sede simulada".
- A gravação foi um `MediaRecorder` substituído no teste, com blob `audio/mpeg` (cabeçalho ID3). O compositor avisou que o navegador não converteu o arquivo e o histórico mostrou "Áudio". ffmpeg não foi chamado.
- O atendente registrou consentimento `cobranca`, vinculou `fake-customer-casa` / `fake-contract-casa`, viu "Cadastro simulado — casa", consultou as faturas e confirmou a segunda via. O painel mostrou "fatura simulada em aberto" e "Segunda via enfileirada". A verificação digitada foi a cadeia local `verificacao-simulada`, não o mecanismo D2-02.
- O receptor devolveu 204 e guardou pelo menos um POST `message.created` cujo HMAC SHA-256 do corpo, com o segredo exibido, coincidiu com `X-ChatISP-Signature-256`.

O log do proxy repetiu o aviso de certificado desconhecido e `http: proxy error: context canceled`. A jornada mesmo assim concluiu.

## Checks desta rodada

- `npm ci --prefix backend/tools/pg --no-audit --no-fund`: passou.
- `npm ci --prefix web --no-audit --no-fund`: passou, 537 pacotes.
- `npm --prefix web run test:e2e:m4b-m6`: passou, sem skip, código 0.
- `npm --prefix web run test:e2e:m3b`: passou, código 0. O runner não imprime frase de sucesso; o processo terminou sem o erro `M3B E2E FALHOU`.
- `npm --prefix web run test:e2e:m4a`: passou, código 0, com a frase `M4A E2E passou`. O seletor do anexo passou a `getByLabel("Arquivo")`. O seletor antigo `input[type=file]:visible` achava também o "Anexar arquivo" do compositor. As asserções do PDF, do download e do 403 permaneceram. Uma tentativa anterior dessa jornada parou no combobox "Caixa" antes de chegar ao anexo; a repetição concluiu.

## Defeitos vistos e não corrigidos aqui

Ficam fora dos arquivos autorizados desta tarefa:

- Mensagens sem `reply_to_message_id` no banco ainda saem no JSON como UUID zero. O painel trata esse valor como citação e mostra "Mensagem citada indisponível" na imagem e na localização. O documento citado não usou esse texto: mostrou "Sede simulada".
- O cartão do documento não mostrou `boleto.pdf`. O conteúdo gravado usa `file_name` e o envio não cria `media_items`, então o painel fica em "Arquivo ainda não disponível." A URL assinada temporária não foi pedida.
- `TestDrillRestoresDumpAndReapplies` continua a falha já registrada: o PostgreSQL embutido parece diretório de checkout real. Este recorte não reexecutou o ensaio.
- ESLint global não foi alterado.
- API pública de escrita, Pix, timeout ambíguo da segunda via, falha do destino do webhook e o segundo cadastro do catálogo fake não entraram na jornada.

## O que permanece dependente de sistema real

Meta Cloud, ERP do provedor e Evolution não foram chamados. A conversão OGG/Opus espera ffmpeg, que não foi instalado nem executado. Nada disto é aceite de produção.
