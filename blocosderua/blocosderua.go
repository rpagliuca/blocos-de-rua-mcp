// Package blocosderua é um cliente da API pública do site blocosderua.com (WordPress multisite):
// a agenda de eventos (desfiles de blocos, shows, festas) de cada cidade, o detalhe de um evento e
// o cadastro de blocos.
//
// Rotas usadas (todas GET, sem autenticação, com cache de 4 h no Cloudflare):
//
//	/<cidade>/wp-json/wp/v2/programacao?per_page=100&page=N  agenda: só eventos futuros, já em ordem de data
//	/<cidade>/wp-json/wp/v2/programacao/<id>                 detalhe de um evento
//	/<cidade>/wp-json/site/v1/blocos                         todos os blocos da cidade (sem paginação)
//	/<cidade>/wp-json/mobile/blocos/<id>                     um bloco e os eventos dele
//
// São Paulo é o site raiz (sem prefixo de cidade).
package blocosderua

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// BaseURL é o endereço do site.
const BaseURL = "https://www.blocosderua.com"

// Cidade de um subsite.
type Cidade struct {
	Codigo  string `json:"codigo"`
	Nome    string `json:"nome"`
	prefixo string
}

// Cidades atendidas pelo site, na ordem do seletor dele.
var Cidades = []Cidade{
	{"sao-paulo", "São Paulo", ""},
	{"rio-de-janeiro", "Rio de Janeiro", "/rio-de-janeiro"},
	{"belo-horizonte", "Belo Horizonte", "/belo-horizonte"},
	{"salvador", "Salvador", "/salvador"},
	{"florianopolis", "Florianópolis", "/florianopolis"},
	{"recife-olinda", "Recife e Olinda", "/recife-olinda"},
	{"brasilia", "Brasília", "/brasilia"},
	{"porto-alegre", "Porto Alegre", "/porto-alegre"},
	{"fortaleza", "Fortaleza", "/fortaleza"},
}

// AcharCidade aceita o código ("rio-de-janeiro") ou o nome, sem diferença de acento e caixa;
// vazio é São Paulo.
func AcharCidade(s string) (Cidade, error) {
	if strings.TrimSpace(s) == "" {
		return Cidades[0], nil
	}
	n := Normalizar(s)
	for _, c := range Cidades {
		if n == Normalizar(c.Codigo) || n == Normalizar(c.Nome) {
			return c, nil
		}
	}
	for _, c := range Cidades { // "recife", "olinda", "sp", "rio"
		if strings.Contains(Normalizar(c.Nome), n) {
			return c, nil
		}
	}
	if n == "sp" {
		return Cidades[0], nil
	}
	nomes := make([]string, len(Cidades))
	for i, c := range Cidades {
		nomes[i] = c.Codigo
	}
	return Cidade{}, fmt.Errorf("cidade %q não atendida pelo blocosderua.com (há: %s)", s, strings.Join(nomes, ", "))
}

var fuso = func() *time.Location {
	l, err := time.LoadLocation("America/Sao_Paulo")
	if err != nil {
		return time.FixedZone("BRT", -3*3600)
	}
	return l
}()

// Evento é um item da agenda: desfile de bloco, show, festa.
type Evento struct {
	ID               int     `json:"id"`
	Titulo           string  `json:"titulo"`
	Data             string  `json:"data"`           // AAAA-MM-DD
	Hora             string  `json:"hora,omitempty"` // HH:MM
	Bairro           string  `json:"bairro,omitempty"`
	Endereco         string  `json:"endereco,omitempty"`
	EnderecoCompleto string  `json:"endereco_completo,omitempty"`
	Lat              float64 `json:"lat,omitempty"`
	Lng              float64 `json:"lng,omitempty"`
	Preco            string  `json:"preco,omitempty"`
	Ingressos        string  `json:"ingressos,omitempty"`
	Link             string  `json:"link"`
	Descricao        string  `json:"descricao,omitempty"`
	Imagem           string  `json:"imagem,omitempty"`
	Cidade           string  `json:"cidade"`
}

// Inicio é a data e hora do evento em São Paulo (meia-noite quando não há hora).
func (e Evento) Inicio() time.Time {
	h := e.Hora
	if h == "" {
		h = "00:00"
	}
	t, err := time.ParseInLocation("2006-01-02 15:04", e.Data+" "+h, fuso)
	if err != nil {
		t, _ = time.ParseInLocation("2006-01-02", e.Data, fuso)
	}
	return t
}

// Bloco é o grupo (o bloco em si), com os eventos dele quando o site os associa.
type Bloco struct {
	ID        int      `json:"id"`
	Nome      string   `json:"nome"`
	Slug      string   `json:"slug"`
	Descricao string   `json:"descricao,omitempty"`
	Link      string   `json:"link"`
	Eventos   []Evento `json:"eventos,omitempty"`
	Cidade    string   `json:"cidade"`
}

// Cliente da API. O zero não serve: use Novo.
type Cliente struct {
	Base  string
	HTTP  *http.Client
	Cache time.Duration // por quanto tempo a agenda e a lista de blocos ficam em memória
	agora func() time.Time

	mu     sync.Mutex
	agenda map[string]guardado[[]Evento]
	blocos map[string]guardado[[]Bloco]
}

type guardado[T any] struct {
	valor T
	em    time.Time
}

// Novo cria o cliente com o site real e 10 min de cache.
func Novo() *Cliente {
	return &Cliente{Base: BaseURL, HTTP: &http.Client{Timeout: 30 * time.Second}, Cache: 10 * time.Minute, agora: time.Now,
		agenda: map[string]guardado[[]Evento]{}, blocos: map[string]guardado[[]Bloco]{}}
}

// ErrNaoEncontrado é devolvido quando o id não existe na cidade.
var ErrNaoEncontrado = errors.New("não encontrado")

func (c *Cliente) get(ctx context.Context, cid Cidade, caminho string, q url.Values, destino any) (http.Header, error) {
	u := c.Base + cid.prefixo + "/wp-json/" + caminho
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "blocos-de-rua-mcp (+https://github.com/rpagliuca/blocos-de-rua-mcp)")
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	corpo, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode == http.StatusNotFound {
		return nil, ErrNaoEncontrado
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("blocosderua.com %s: HTTP %d", caminho, resp.StatusCode)
	}
	if err := json.Unmarshal(corpo, destino); err != nil {
		return nil, fmt.Errorf("blocosderua.com %s: resposta inesperada: %w", caminho, err)
	}
	return resp.Header, nil
}

// ---------- agenda ----------

// texto é um campo que a API devolve às vezes como string, às vezes como número ou false.
type texto string

func (t *texto) UnmarshalJSON(b []byte) error {
	var s string
	if json.Unmarshal(b, &s) == nil {
		*t = texto(s)
		return nil
	}
	var n json.Number
	if json.Unmarshal(b, &n) == nil {
		*t = texto(n.String())
		return nil
	}
	*t = ""
	return nil
}

type acfEvento struct {
	Data     texto `json:"data"`
	Hora     texto `json:"hora"`
	Endereco texto `json:"endereco"`
	Geo      *struct {
		Address texto   `json:"address"`
		Lat     float64 `json:"lat"`
		Lng     float64 `json:"lng"`
	} `json:"geolocalizacao"`
	Preco   texto `json:"preco"`
	Bairro  texto `json:"bairro"`
	Comprar texto `json:"comprar"`
}

// acfOuNada aceita o objeto ACF ou false/[] (posts sem campos).
type acfOuNada struct{ acfEvento }

func (a *acfOuNada) UnmarshalJSON(b []byte) error {
	if len(b) > 0 && b[0] == '{' {
		return json.Unmarshal(b, &a.acfEvento)
	}
	return nil
}

type rendered struct {
	Rendered string `json:"rendered"`
}

type postWP struct {
	ID      int       `json:"id"`
	Slug    string    `json:"slug"`
	Link    string    `json:"link"`
	Title   rendered  `json:"title"`
	Content rendered  `json:"content"`
	Imagem  texto     `json:"featured_image_src"`
	ACF     acfOuNada `json:"acf"`
}

func (p postWP) evento(cid Cidade) Evento {
	a := p.ACF.acfEvento
	e := Evento{ID: p.ID, Titulo: Limpar(p.Title.Rendered), Data: dataISO(string(a.Data)), Hora: string(a.Hora),
		Bairro: strings.TrimSpace(string(a.Bairro)), Endereco: strings.TrimSpace(string(a.Endereco)),
		Preco: strings.TrimSpace(string(a.Preco)), Ingressos: strings.TrimSpace(string(a.Comprar)),
		Link: p.Link, Descricao: Limpar(p.Content.Rendered), Imagem: string(p.Imagem), Cidade: cid.Codigo}
	if a.Geo != nil {
		e.EnderecoCompleto, e.Lat, e.Lng = string(a.Geo.Address), a.Geo.Lat, a.Geo.Lng
	}
	return e
}

// dataISO converte AAAAMMDD em AAAA-MM-DD.
func dataISO(s string) string {
	if len(s) == 8 {
		return s[:4] + "-" + s[4:6] + "-" + s[6:]
	}
	return s
}

// Agenda devolve todos os eventos futuros da cidade, em ordem de início.
func (c *Cliente) Agenda(ctx context.Context, cid Cidade) ([]Evento, error) {
	c.mu.Lock()
	g, ok := c.agenda[cid.Codigo]
	c.mu.Unlock()
	if ok && c.agora().Sub(g.em) < c.Cache {
		return g.valor, nil
	}
	var todos []Evento
	for pagina := 1; ; pagina++ {
		var posts []postWP
		q := url.Values{"per_page": {"100"}, "page": {strconv.Itoa(pagina)}}
		h, err := c.get(ctx, cid, "wp/v2/programacao", q, &posts)
		if err != nil {
			return nil, err
		}
		for _, p := range posts {
			todos = append(todos, p.evento(cid))
		}
		total, _ := strconv.Atoi(h.Get("X-WP-TotalPages"))
		if pagina >= total || len(posts) == 0 || pagina >= 50 {
			break
		}
	}
	sort.SliceStable(todos, func(i, j int) bool { return todos[i].Inicio().Before(todos[j].Inicio()) })
	c.mu.Lock()
	c.agenda[cid.Codigo] = guardado[[]Evento]{todos, c.agora()}
	c.mu.Unlock()
	return todos, nil
}

// Evento devolve o detalhe de um evento pelo id.
func (c *Cliente) Evento(ctx context.Context, cid Cidade, id int) (Evento, error) {
	var p postWP
	if _, err := c.get(ctx, cid, "wp/v2/programacao/"+strconv.Itoa(id), nil, &p); err != nil {
		return Evento{}, err
	}
	return p.evento(cid), nil
}

// EventoPeloSlug acha um evento pelo slug (o fim do link, como falamansa-sp-27-11-26).
func (c *Cliente) EventoPeloSlug(ctx context.Context, cid Cidade, slug string) (Evento, error) {
	var posts []postWP
	if _, err := c.get(ctx, cid, "wp/v2/programacao", url.Values{"slug": {slug}}, &posts); err != nil {
		return Evento{}, err
	}
	if len(posts) == 0 {
		return Evento{}, ErrNaoEncontrado
	}
	return posts[0].evento(cid), nil
}

// ---------- blocos ----------

type postCustom struct {
	ID          texto           `json:"ID"`
	Titulo      string          `json:"post_title"`
	Slug        string          `json:"post_name"`
	Conteudo    string          `json:"post_content"`
	URL         string          `json:"url"`
	Programacao json.RawMessage `json:"programacao"`
}

func (p postCustom) bloco(cid Cidade) Bloco {
	id, _ := strconv.Atoi(string(p.ID))
	link := p.URL
	if p.Slug != "" {
		link = BaseURL + cid.prefixo + "/blocos/" + p.Slug + "/"
	}
	return Bloco{ID: id, Nome: Limpar(p.Titulo), Slug: p.Slug, Descricao: Limpar(p.Conteudo), Link: link, Cidade: cid.Codigo}
}

// Blocos devolve o cadastro de blocos da cidade (sem os eventos).
func (c *Cliente) Blocos(ctx context.Context, cid Cidade) ([]Bloco, error) {
	c.mu.Lock()
	g, ok := c.blocos[cid.Codigo]
	c.mu.Unlock()
	if ok && c.agora().Sub(g.em) < c.Cache {
		return g.valor, nil
	}
	var posts []postCustom
	if _, err := c.get(ctx, cid, "site/v1/blocos", nil, &posts); err != nil {
		return nil, err
	}
	bs := make([]Bloco, 0, len(posts))
	for _, p := range posts {
		bs = append(bs, p.bloco(cid))
	}
	c.mu.Lock()
	c.blocos[cid.Codigo] = guardado[[]Bloco]{bs, c.agora()}
	c.mu.Unlock()
	return bs, nil
}

// Bloco devolve um bloco pelo id, com os eventos que o site associa a ele.
func (c *Cliente) Bloco(ctx context.Context, cid Cidade, id int) (Bloco, error) {
	var p postCustom
	if _, err := c.get(ctx, cid, "mobile/blocos/"+strconv.Itoa(id), nil, &p); err != nil {
		return Bloco{}, err
	}
	if string(p.ID) == "" {
		return Bloco{}, ErrNaoEncontrado
	}
	b := p.bloco(cid)
	var eventos []postCustomEvento
	if json.Unmarshal(p.Programacao, &eventos) == nil {
		for _, e := range eventos {
			b.Eventos = append(b.Eventos, e.evento(cid))
		}
	}
	return b, nil
}

// postCustomEvento é um evento no formato das rotas site/ e mobile/ (campos do WP_Post + acf).
type postCustomEvento struct {
	ID       texto     `json:"ID"`
	Titulo   string    `json:"post_title"`
	Conteudo string    `json:"post_content"`
	URL      string    `json:"url"`
	ACF      acfOuNada `json:"acf"`
	Imagem   texto     `json:"thumbnail_lg"`
}

func (p postCustomEvento) evento(cid Cidade) Evento {
	id, _ := strconv.Atoi(string(p.ID))
	wp := postWP{ID: id, Link: p.URL, Title: rendered{p.Titulo}, Content: rendered{p.Conteudo}, Imagem: p.Imagem, ACF: p.ACF}
	return wp.evento(cid)
}

// ---------- texto ----------

var (
	reTag    = regexp.MustCompile(`(?s)<[^>]*>`)
	reBr     = regexp.MustCompile(`(?i)<br\s*/?>|</p>`)
	reEspaco = regexp.MustCompile(`[ \t\r\f\v]+`)
	reLinhas = regexp.MustCompile(`\n\s*\n+`)
	// Rodapé que o site cola em todo evento ("Siga nosso instagram @blocosderuasp, nosso ...").
	reSiga = regexp.MustCompile(`(?is)\n?\s*siga noss[oa]s? (instagram|redes).*$`)
)

// Limpar tira o HTML, as entidades e o rodapé de redes sociais.
func Limpar(s string) string {
	s = reBr.ReplaceAllString(s, "\n")
	s = reTag.ReplaceAllString(s, "")
	s = html.UnescapeString(s)
	s = strings.ReplaceAll(s, " ", " ")
	s = reEspaco.ReplaceAllString(s, " ")
	s = reLinhas.ReplaceAllString(s, "\n\n")
	s = reSiga.ReplaceAllString(s, "")
	return strings.TrimSpace(s)
}

var semAcento = strings.NewReplacer(
	"á", "a", "à", "a", "â", "a", "ã", "a", "ä", "a",
	"é", "e", "ê", "e", "è", "e", "ë", "e",
	"í", "i", "î", "i", "ì", "i", "ï", "i",
	"ó", "o", "ô", "o", "õ", "o", "ò", "o", "ö", "o",
	"ú", "u", "û", "u", "ù", "u", "ü", "u",
	"ç", "c", "ñ", "n",
)

// Normalizar põe em minúsculas, sem acento e com hífens como espaço, para comparar nomes.
func Normalizar(s string) string {
	s = semAcento.Replace(strings.ToLower(strings.TrimSpace(s)))
	s = strings.NewReplacer("-", " ", "_", " ").Replace(s)
	return strings.Join(strings.Fields(s), " ")
}
