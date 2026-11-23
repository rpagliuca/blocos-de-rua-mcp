# blocos-de-rua-mcp

Cliente da API pública do [blocosderua.com](https://www.blocosderua.com/) (agenda de blocos de rua,
shows e festas de nove capitais) e ferramentas MCP em cima dele. O uso principal é dentro do
`automacoes-residenciais-mcp`, que importa o pacote `ferramentas` e chama `Registrar`; o comando
`blocos-de-rua-mcp` serve as mesmas ferramentas por stdio e funciona como CLI de teste.

## Ferramentas

- `agenda_blocos_de_rua`: próximos eventos de uma cidade, com filtros de data (`de`, `ate`:
  `AAAA-MM-DD`, `DD/MM`, `hoje`, `amanha`, `fim_de_semana`), região (`bairros`, ou `perto_de`
  `lat,lng` com `raio_km`) e `texto`. Devolve id, título, data, dia da semana, hora, bairro,
  endereço, preço e link.
- `detalhe_bloco_de_rua`: um evento pelo id ou pelo link, com descrição, endereço completo,
  coordenadas, preço e link de ingresso.
- `buscar_bloco_de_rua`: o bloco (o grupo) pelo nome, com os eventos que o site associa a ele.

Cidades: `sao-paulo` (padrão), `rio-de-janeiro`, `belo-horizonte`, `salvador`, `florianopolis`,
`recife-olinda`, `brasilia`, `porto-alegre`, `fortaleza` (aceita o nome, sem diferença de acento).

## A API do site

O site é um WordPress multisite atrás do Cloudflare (cache de 4 h). São Paulo é o site raiz e cada
outra cidade é um subsite (`/rio-de-janeiro/`, …). As páginas são renderizadas no servidor; os
dados saem da REST do WordPress, sem autenticação:

| Rota | O que dá |
|---|---|
| `GET /<cidade>/wp-json/wp/v2/programacao?per_page=100&page=N` | Agenda: só eventos futuros, em ordem de data; `X-WP-Total` e `X-WP-TotalPages` nos headers. `search=` e `slug=` funcionam. |
| `GET /<cidade>/wp-json/wp/v2/programacao/<id>` | Detalhe de um evento. |
| `GET /<cidade>/wp-json/site/v1/blocos` | Cadastro de blocos da cidade, inteiro (1.207 em São Paulo, ~360 KB). |
| `GET /<cidade>/wp-json/mobile/blocos/<id>` | Um bloco e os eventos dele (`programacao`). |

Os campos do evento ficam em `acf`: `data` (`AAAAMMDD`), `hora` (`HH:MM`), `endereco`,
`geolocalizacao` (`address`, `lat`, `lng`), `preco`, `bairro` e `comprar` (link de ingresso). O
texto (`content.rendered`) é HTML e termina com o rodapé de redes sociais do site, que o cliente
tira.

Rotas que existem mas não servem: `site/v1/programacao` (no máximo 10, ignora parâmetros) e
`mobile/programacao`, `mobile/v2/programacao`, `mobile/programacao/datas`,
`mobile/programacao/bairros`, `mobile/v2/blocos/<id>` e a lista `mobile/blocos`, que respondiam 500
("erro crítico" do WordPress) em 23/11/2026, com ou sem parâmetros.

Fora da temporada a agenda é curta (10 eventos em São Paulo em 23/11/2026); ela enche perto do
Carnaval. A agenda e a lista de blocos ficam 10 min em memória.

## Uso

```sh
make test build
./blocos-de-rua-mcp agenda sao-paulo fim_de_semana
./blocos-de-rua-mcp detalhe https://www.blocosderua.com/programacao/falamansa-sp-27-11-26/
./blocos-de-rua-mcp bloco "baixo augusta"
./blocos-de-rua-mcp json agenda_blocos_de_rua '{"bairros":["Pinheiros","Vila Madalena"],"de":"2027-02-06","ate":"2027-02-17"}'
```
