# Verificação de domínio — 06/11/2026

Na instalação informada como versão `8150c9b`, com migração 57 e MFA opcional, a verificação foi executada a partir deste servidor usando `scripts/check-public-domain.mjs`. O teste com DNS normal recebeu `ECONNREFUSED` nas conexões HTTP e HTTPS. Esse teste automatizado não comprova o acesso público externo e não identifica sozinho a causa da recusa. Em seguida, o proprietário confirmou que a página ChatISP abre em outra rede, respondendo à pergunta sobre teste externo (por exemplo, 4G/5G sem Wi-Fi). A confirmação manual cobre a página; não se declara automação autenticada externa.

Com o nome `chatisp.tokstiloesporte.com` resolvido explicitamente para o proxy interno `10.10.0.10`, passaram:

- Redirecionamento HTTP para HTTPS e certificado validado pela confiança padrão do Node, com o nome do domínio.
- Página HTML de entrada, `/healthz` e `/readyz` com estados JSON esperados; `/metrics` retornou 404.
- GET sem assinatura à raiz do bucket privado retornou 403; PUT na rota do bucket retornou 404.
- Login da conta administrativa sem MFA ativo, leitura da sessão e atributos do cookie; escrita sem CSRF, escrita com Origin estranho e WebSocket com Origin estranho foram recusados; logout retornou 204. Nenhum cadastro de MFA foi iniciado.

A resolução substituída prova o caminho ao proxy interno e não prova DNS ou conectividade externa. Os testes acima não incluem uma nova jornada de atendimento, entrega WebSocket ou upload e leitura de anexo assinado; evidências anteriores desses fluxos constam em `docs/medicoes/publicacao-20261106/verificacao.txt`.

Para repetir a verificação passiva pelo DNS normal, use:

```sh
node scripts/check-public-domain.mjs --origin https://chatisp.tokstiloesporte.com --bucket chatisp-anexos
```

Acrescente `--resolve 10.10.0.10` para repetir pela rota interna. O par opcional `--email` e `--password-file` testa login e encerra a sessão; uma conta que exigir MFA é identificada pelo desafio e o teste autenticado é omitido. O login só ocorre após todas as verificações passivas passarem; o PUT de verificação só ocorre após confirmar o painel, saúde, prontidão e a recusa da leitura anônima do bucket. A ferramenta não solicita código, não inicia cadastro de MFA e não desativa validação TLS.

Após acrescentar esses bloqueios, uma fixture TLS isolada simulou falha de saúde e confirmou zero requisições de login e zero PUT; argumentos inválidos também foram recusados. A fixture usou uma CA temporária de teste, sem alterar a confiança TLS padrão usada na verificação real descrita acima.
