# Pacote de referência para o novo design do ChatISP

**Data:** 07/11/2026, America/Fortaleza. **Referência do código:** `2730364a4aa547245c111c3c6f5ba310d5058157`.

## Ordem de leitura

1. **CHATISP_INVENTARIO_COMPLETO_PARA_DESIGN.md** — descrição do produto de ponta a ponta: papéis, rotas, jornadas, módulos, campos, ações, estados, limites e recursos ausentes.
2. **CHECKLIST_TELAS_COMPONENTES_ESTADOS.md** — 142 unidades de conferência para não esquecer partes do design. A versão CSV permite acompanhar a cobertura em planilha.
3. **ANEXO_MATRIZ_ORIGINAL_257_REQUISITOS.md** — cópia integral da matriz vigente. A versão CSV preserva suas 257 linhas.

Os 142 itens de UX não são uma contagem alternativa dos 257 requisitos do projeto. Um requisito técnico pode não exigir tela; um módulo pode precisar de várias variações visuais.

A matriz original guarda evidências e textos históricos. Para decidir o que desenhar como capacidade atual, usar a disponibilidade e os limites descritos no inventário confrontado com o código. Não transformar previsão V2 ou integração simulada em função pronta.

## Uso recomendado

O ChatISP é uma central de atendimento de provedores, com vários operadores. O canal do cliente na V1 é WhatsApp. Não é um novo ERP, widget para site, central de telefonia ou uma reprodução completa do Chatwoot.

O designer pode reorganizar telas e simplificar a apresentação de campos técnicos. Precisa manter acesso às funções e respeitar os papéis. Um componente visual que dependa de dado/endpoint ainda inexistente deve ser indicado como mudança de produto/implementação.

## Texto para acompanhar o envio à IA ou ao designer

> Redesenhe o ChatISP usando o inventário anexo como referência funcional. Leia primeiro a descrição completa e depois a checklist de 142 itens. Use a matriz de 257 requisitos como conferência adicional, distinguindo infraestrutura de interface.
>
> O produto é uma central web de atendimento para provedores de internet, com vários atendentes, supervisores e administradores. Não é um ERP nem um widget de chat de visitantes. Aproveite referências visuais, mas não copie funcionalidades de outros produtos como se existissem aqui.
>
> Cubra desktop e celular, as seis áreas principais, as áreas administrativas por papel, o ciclo inteiro de atendimento e os estados vazios, carregando, com erro, sem permissão, desconectado e com resultado incerto. Preserve notas privadas, protocolos/ciclos, posse, capacidade, privacidade e distinção entre aceite, entrega e leitura.
>
> Mostre como cada item da checklist estará acessível. Não retire uma função porque ela não coube na imagem principal: organize-a em contexto, diálogo, seção ou tela apropriada.
>
> Diferencie claramente: função já implementada; integração/configuração condicionante; capacidade parcial; proposta nova. Não invente countdown de janela WhatsApp, estado consolidado de consentimento, prévia de mensagem na fila, ERP real, QR de Evolution, transcrição por IA ou biblioteca global de anexos como se já estivessem disponíveis.
>
> Entregue arquitetura de informação, mapa de navegação por papel, jornadas, telas/componentes e variações desktop/mobile, além da checklist preenchida com a localização de cada recurso. Propostas de funções novas devem ficar identificadas e separadas da cobertura do produto atual.

## Limites e preservação

Este material foi produzido por leitura e consolidação. Não iniciou carga de 1.000 atendimentos, não alterou integrações e não consumiu assinaturas de agentes externos. Não inclui credenciais, dados de clientes reais ou backup do banco.
