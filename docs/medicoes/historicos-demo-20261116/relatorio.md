# Dados fictícios e histórico de atendimento — 16/11/2026

Carga adicional autorizada pelo proprietário na empresa demonstrativa já existente. Não altera aplicação, contas, senhas nem registros anteriores. Não exige atualização de imagens.

- 24 contatos novos com nomes identificados por `DEMO` e e-mails em `exemplo.invalid`.
- 96 conversas novas: três encerradas e uma atual por contato (72 encerradas, 24 atuais).
- 768 mensagens históricas, 96 notas internas e tags de demonstração/assunto.
- Casos de Wi-Fi, quedas, financeiro e comercial, distribuídos nos três setores existentes.
- Conversas atuais atribuídas ao administrador e aos atendentes, além de seis casos sem responsável. Ana recebe seis conversas novas em Minhas.

Prefixo dos protocolos: `DEMO-HIST-20261116`. Caixa: **DEMONSTRAÇÃO — Atendimento do provedor**. Para consultar: entrar como Dono da empresa ou Atendimento → Conversas → selecionar essa caixa → Minhas ou Todas. Abrir um contato `DEMO · ...` e consultar Integrações → Histórico do contato. Para ler a conversa encerrada, buscar o protocolo na fila e usar Filtros → Status → Encerrada. Clique fora dos filtros para voltar à lista. Relatórios com intervalo de 30 dias incluem os exemplos encerrados mais recentes.

## Operação e evidências

Script: `scripts/seed-demo-history.py`. Sem `--apply`, apenas descreve a carga. A escrita usa transação, IDs determinísticos e INSERT ON CONFLICT DO NOTHING, sem UPDATE/DELETE. Exige a caixa demonstrativa existente, conector desconectado/desativado e ausência de canal externo habilitado no provedor. A repetição foi conferida sem duplicar as 96 conversas, 24 contatos e 96 notas.

```sh
python3 scripts/seed-demo-history.py --project chatisp-pronto-hoje --dataset /home/enok/.local/share/chatisp/demonstracao-20261107/dataset.json
# Acrescentar --apply somente para aplicar a carga autorizada.
```

Todas as 768 mensagens da carga têm `historical=true`; nenhuma possui tentativa de envio em `outbound_attempts`. Mensagens digitadas posteriormente durante uso da demonstração são registros separados e foram preservadas. Não foram criados boletos, anexos, chamadas a ERP, WhatsApp ou Evolution. Nenhum conector foi ativado. Os tempos destas mensagens importadas não constituem prova de desempenho operacional.

Prova Chromium com login real dos perfis Dono da empresa e Atendimento: conversa atual e suas mensagens visíveis, histórico com quatro protocolos (três encerrados) e abertura/leitura de mensagens da conversa encerrada. Prova pelo domínio resolvido ao proxy interno `10.10.0.10`, com TLS normal. Logs e capturas privados em `.tmp/demo-history-release/` (`browser.txt`, `browser-agent-final.txt`, `seed-messages-proof.json` e capturas). Manifesto privado: `/home/enok/.local/share/chatisp/demonstracao-20261107/historicos-20261116.json`.

Backup anterior: `/home/enok/.local/share/chatisp/backups-publicacao/snapshot-20261116T145836Z-KxEUcxAR`, sete checksums verificados, contendo banco, Garage, configuração/credenciais e imagens. Não foi repetida restauração integral. Recuperação conforme [procedimento existente](../publicacao-20261106/backup.md). Não restaurar sobre a instalação ativa nem apagar em lote por prefixo: dados adicionados/interações posteriores precisam ser preservados.
