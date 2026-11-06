# Prontidão local — 06/11/2026

Data local: America/Fortaleza. Base limpa `a0103d4`, mesma branch `continuation/cursor-integracao-2`. Código integrado no commit local `0002570`, com fontes de execução idênticas às verificadas. Os commits anteriores foram preservados; não houve push, publicação de domínio, alteração de outro projeto, Paseo/Agent Control nem conexão com fornecedor real.

O usuário pediu uma nova revisão até deixar o sistema utilizável hoje, autorizando decisões técnicas e adiamento de domínio/serviços indisponíveis. A entrega é uma **instalação local persistente, disponível para primeiro acesso**. WhatsApp, ERP, Evolution e SMTP externos permanecem desconectados. Não foi transformada simulação em integração nem declarada produção homologada.

## Instalação entregue

- Projeto Compose: `chatisp-pronto-hoje`, com PostgreSQL 18.6 Alpine, Garage, API, worker e Caddy saudáveis.
- Acesso no próprio servidor: **https://localhost:8443/entrar**, certificado da CA local.
- Administrador inicial: `admin@chatisp.local`, papel de administrador do provedor, sem superadmin. MFA obrigatório no primeiro acesso.
- Credenciais: `/home/enok/.local/share/chatisp/pronto-hoje-20261106/acesso.txt`, arquivo 0600 em diretório 0700. Senha e chaves não aparecem neste relatório, nos logs publicados ou no Git.
- Configuração persistente: `ambiente.env`, `rede.yaml` e `estado.json` no mesmo diretório privado. Sub-rede local escolhida após conferir rotas e redes existentes; nenhuma rede alheia foi alterada.
- Plano local escolhido: 1 GiB de anexos e fuso America/Fortaleza. É configuração desta instalação local, não capacidade ou compromisso comercial.
- Apenas `127.0.0.1:8443` está publicado. PostgreSQL, Garage e métricas ficam na rede interna; `/metrics` público responde 404.

O runtime usa `CHATISP_ENV=production` para manter seus controles, mas o perfil Alpine e a CA interna são de instalação local, sem homologação de produção. Meta oficial está desligada, SMTP não está configurado e não existe caixa externa conectada. Convites e recuperação por e-mail ficam indisponíveis até haver SMTP; o primeiro administrador funciona pelo bootstrap.

Operação da instalação sem revelar configuração:

```sh
python3 /home/enok/.local/share/chatisp/pronto-hoje-20261106/controle.py ps
python3 /home/enok/.local/share/chatisp/pronto-hoje-20261106/controle.py stop
python3 /home/enok/.local/share/chatisp/pronto-hoje-20261106/controle.py up -d --no-build --wait
```

`stop` preserva os volumes. O controlador é um atalho operacional gerado para os caminhos desta instalação; as definições de serviço estão versionadas em `deploy/compose.yaml` e `deploy/compose.local-alpine.yaml`. Preservar o diretório privado junto dos backups é necessário para recuperar chaves e configuração. Destino independente/cofre e operação comercial continuam na tabela oficial de dependências.

## Falhas encontradas e corrigidas

| Problema reproduzido | Correção e evidência |
| --- | --- |
| Assumir, transferir, devolver ou encerrar mantinha o adiamento ativo | Resolução e evento de snooze no mesmo commit da transição; testes PostgreSQL incluem versão obsoleta, replay e vencimento posterior. |
| Desativação/suspensão podia confirmar antes de revogar sessões; login concorrente podia escapar | Revogação na transação adequada e emissão sob o mesmo lock do tenant. A migração `00056` confere senha, estado/cifra MFA e superadmin sob lock da linha do usuário até inserir a sessão. |
| Login com senha/fator já revogados podia emitir sessão depois do reset | Comparação do snapshot comprovado sob lock, serializada com o UPDATE de reset. Provas determinísticas de senha e MFA passaram. |
| Nova serialização poderia esgotar a pool por checkout aninhado | Contexto auth reutiliza a transação/conexão tenant; plataforma revoga por função específica na própria transação. Testes com pool limitada e saturação passaram; privilégios e scopes foram conferidos. |
| Conversão recusada não persistia o motivo; duas conversões duplicavam saída/quota | Recusa confirma antes de retornar erro; intenção durável precede publicação. Lock do item serializa a saída e a perdedora libera sua reserva; rollback após Create deixa intenção limpável. Testes e race passaram. |
| Falha de download alterava mídia depois da suspensão | Revalidação do tenant no término; falha definitiva sem objeto pendente termina, estado incerto com intenção continua recuperável. |
| JSON aceitava conteúdo adicional ou excedente ignorado; texto válido não cabia nos envelopes | EOF obrigatório após um objeto. Texto final mantém 16000 bytes UTF-8 incluindo assinatura; envio e despedida têm JSON de 128 KiB e idempotência, 256 KiB. Unicode, escapes, assinatura, overflow e replay foram testados. |
| Exportação de livro interrompida podia passar despercebida; aliases de arquivos ameaçavam a configuração do restore | Marcador persistente de exportação em curso; validação de `--env-file`, saída das chaves e limites numéricos antes de parar serviços. Python: 24 testes. |
| Interrupção do validador podia recriar recursos após cleanup | Supervisor encerra e aguarda seu grupo de processos antes do down; teste de SIGTERM e duas provas reais concluídas sem sobras. |
| Erros em equipes/atribuição não tinham retry e seleção de setor mantinha vínculo anterior | Nova tentativa e limpeza/bloqueio do contexto de seleção durante alteração; Vitest e auditoria de navegador passaram. |
| Launcher padrão HTTP não autenticava cookies seguros; primeira configuração era manual e sujeita a placeholders | HTTPS padrão, opt-out apenas para diagnóstico, gerador 0600 sem sobrescrita e perfil Alpine local. Build, bootstrap e prova HTTPS da instalação real passaram. |
| Ferramenta de ensaio interpretava contador irrelevante de I/O e não parametrizava o cenário | Parser corrigido; cenário sintético com limites de ferramenta, recusa antecipada de valores inválidos/DSNs externos e execução sem baixar módulos/toolchain. Sem alegação de capacidade. |

Revisores distintos conferiram atendimento/API, autenticação e publicação de mídia. As corridas de pool e MFA apontadas durante a revisão foram reproduzidas e corrigidas antes dos gates finais; não ficaram como ressalva genérica.

## Verificações

[testes.txt](testes.txt) registra os comandos e escopos: Go amplo em 66 pacotes, vet/lint/gerados, race em cinco pacotes críticos, Vitest 61 arquivos/297 testes, Python 24, jornadas M3B/M4B–M6/M7 e UX com 52 visões sem falha. O teste do supervisor por SIGTERM também passou. O lint inicial encontrou um helper sem uso após a refatoração de mídia; ele foi removido e o gate completo repetido com sucesso.

A prova final **`chatisp-validate-3000114`** passou com saída 0: `CHATISP_VALIDATE_ALPINE=1 CHATISP_VALIDATE_BACKUP=1 ./scripts/compose-validate.sh`. Inclui MFA, SMTP TLS local, atendimento/WebSocket, anexos privados, suspensão, queda/retorno do storage, backup, limpeza retomável e perda/recuperação de volumes/chaves. A prova anterior `chatisp-validate-2797501` também passou, antes do ajuste final dos limites de transporte. Resumo sem segredos em [compose-final.txt](compose-final.txt). Debian continua evidência histórica; esta rodada usa Alpine/Docker 20.10.9.

A instalação persistente foi construída e iniciada separadamente. A prova HTTPS validou health/ready 200, painel servido, métricas 404, login do administrador com cookie Secure/HttpOnly, MFA obrigatório e logout 204. Não foi cadastrado fator MFA automaticamente em nome do usuário. A observação pelo operador e a configuração de conexões externas continuam sendo etapas humanas reais.

O inventário [recursos.txt](recursos.txt) confirma zero contêineres, volumes, redes ou etiquetas das duas provas descartáveis. A instalação `chatisp-pronto-hoje` permanece intencionalmente ativa e seus dados/segredos foram preservados. Nenhuma limpeza global ou reinício do Docker foi executado.

A matriz segue em 206 verificados, 32 aguardando homologação e 19 parciais. A [tabela única de dependências](../../dependencias-externas.md) preserva contratos, credenciais, regras e aceites ainda ausentes, adiados nesta entrega local. Expurgo automático e envio oficial continuam desligados. Essa contagem não declara implementação completa dos recursos externos.
