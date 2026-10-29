# Painel web do Chat ISP

Aplicação estática do posto de atendimento. Seis áreas: Painel, Conversas, Contatos, Equipes, Relatórios e Configurações. Nesta etapa as áreas abrem vazias: não há conversa, contato, equipe nem número de exemplo.

O código e os identificadores ficam em inglês. A interface fica em português do Brasil.

## Comandos

Use o npm 11 que acompanha o Node 24. O npm 10 de `/usr/local/bin` não deve gravar o `package-lock.json`.

```sh
npm ci
npm run dev
npm run lint
npm run typecheck
npm run test
npm run build
npm run generate:api
```

`generate:api` lê `../backend/api/openapi.json` e grava `src/api/schema.ts`. Não copie esses tipos à mão.

O contrato publicado inclui `/healthz`, `/readyz` e a API do painel em `/api/panel` e `/api/platform`. Fila, diretório, equipe e relatório continuam vazios: essas coleções ainda não existem no OpenAPI. Entrar, convite, recuperação de senha, MFA e a administração de usuários e provedores usam o cliente gerado. O cookie de sessão vai com a requisição; o token de CSRF sai no cabeçalho `X-CSRF-Token`. Ao sair, ao trocar de provedor ou quando o servidor recusa a sessão, o painel limpa a consulta, o estado local e o armazenamento do navegador.
