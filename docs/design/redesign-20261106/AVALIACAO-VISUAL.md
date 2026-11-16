# Comparação visual — ChatISP e Chatwoot

06/11/2026. ChatISP examinado no navegador real autenticado, na instalação https://chatisp.tokstiloesporte.com, em desktop 1440×1000 e celular 390×844. Capturadas páginas Painel e Conversas; não foram criados canais, contatos ou mensagens. A instalação não tem caixas disponíveis para essa conta.

Chatwoot examinado pela captura oficial versionada .github/screenshots/dashboard.png, commit 515f9e09b7a2160d99454b6852154509c1a2cf1a, não pela aplicação instalada. Logo, não existe nesta rodada comparação interativa equivalente, mobile Chatwoot ou ensaio de conversa preenchida nas duas aplicações.

## Parecer visual

Na referência examinada, o Chatwoot apresenta composição visual mais elaborada: separação entre navegação, lista de conversas, histórico/compositor e detalhes do contato; hierarquia tipográfica; uso de cor para contexto; ações agrupadas; detalhes secundários recolhíveis. Isso não significa que todos os elementos mostrados sejam Community ou devam ser copiados.

O ChatISP publicado parece uma base funcional que ainda precisa de acabamento de produto. A ausência de caixas impede julgar balões, histórico, mídia e compositor em funcionamento. A instalação vazia explica a falta de conversas, mas não explica os indicadores sem composição visual nem o texto de configuração no cabeçalho.

## Achados independentes da quantidade de dados

1. Painel: indicadores aparecem como linhas de texto com peso visual semelhante, sem diferenciação suficiente entre rótulo, número e urgência. Os valores ficam concentrados no canto superior esquerdo e deixam grande área vazia.
2. Cabeçalho: mostra “Provedor em sessão. O nome desta aba ainda não foi definido.”. Informação incompleta de configuração está ocupando uma área central de produto.
3. Presença e capacidade: seletor de presença e ocupação ficam soltos no rodapé do menu, com aparência de controle nativo e pouca organização. Recomenda-se um bloco compacto de operador/status.
4. Estado vazio: “Nenhuma caixa disponível.” não explica se falta configuração, vínculo ou permissão, nem apresenta um próximo passo adequado ao papel do usuário. Administradores e atendentes devem receber orientações distintas.
5. Hierarquia: componentes globais da navegação têm estilo, mas parte do conteúdo usa composição muito básica e pouco consistente com esse acabamento.
6. Mobile: a navegação vira uma faixa horizontal e alguns itens ficam fora da área visível. O cabeçalho e os controles consomem parte relevante da altura antes do conteúdo. Não foi demonstrado que exista impossibilidade de rolar/acessar esses itens; o achado é de descoberta e uso do espaço, não uma alegação de overflow global não medido.

## Direção recomendada

- Preservar backend, permissões, protocolos e componentes funcionais; trabalhar o frontend.
- Definir tokens e componentes comuns para tipografia, espaços, controles e superfícies.
- No painel, organizar poucos indicadores úteis com números em destaque e acesso às respectivas filas; evitar gráficos decorativos sem dados úteis.
- Na conversa desktop, priorizar lista de atendimentos, mensagem e compositor; detalhes de contato/ERP recolhíveis.
- No celular, trabalhar uma etapa por vez: lista -> conversa -> detalhes, com navegação compacta.
- Dar ao estado vazio instrução contextual e ação permitida, sem criar dados de demonstração silenciosamente.
- Avaliar uma conversa sintética real em ambiente isolado antes de aprovar o desenho do compositor/histórico. Comparar com a mesma tarefa no Chatwoot instalado caso se deseje medir produtividade relativa.

Não foi identificada a causa técnica do acabamento básico: esta rodada não conclui se há estilos ausentes, componentes provisórios ou decisão de implementação. Não houve alteração no produto.

## Capturas

- chatisp-login-desktop.png
- chatisp-painel-desktop.png
- chatisp-painel-mobile.png
- chatisp-conversas-desktop.png
- chatisp-conversas-mobile.png
- chatwoot-referencia.png — captura oficial, não sessão executada nesta rodada.
