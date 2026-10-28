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

`generate:api` lê `../backend/api/openapi.json` e grava `src/api/schema.ts`. O contrato publicado tem `/healthz` e `/readyz`. Essas rotas não alimentam as áreas: fila, diretório, equipe e relatório continuam vazios até existirem no OpenAPI.
