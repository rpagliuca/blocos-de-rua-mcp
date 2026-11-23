// Package ferramentas expõe a agenda do blocosderua.com como ferramentas MCP, para registrar num
// servidor existente (o MCP de smart home) ou servir sozinho por stdio.
package ferramentas

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/rpagliuca/blocos-de-rua-mcp/blocosderua"
)

// Instrucoes explica as ferramentas ao modelo; quem registra junta às instruções do servidor.
const Instrucoes = `Blocos de rua e eventos do blocosderua.com (São Paulo, Rio de Janeiro, Belo Horizonte, Salvador, Florianópolis, Recife e Olinda, Brasília, Porto Alegre e Fortaleza; padrão São Paulo).
agenda_blocos_de_rua: próximos eventos (desfiles de blocos, shows, festas) com filtros de data (de, ate, AAAA-MM-DD ou hoje, amanha, fim_de_semana), região (bairro; ou perto_de lat,lng com raio_km) e texto. O site só publica o que ainda vai acontecer e enche perto do Carnaval; fora da temporada a agenda é curta.
detalhe_bloco_de_rua: tudo de um evento (descrição, endereço completo, coordenadas, preço, link de ingresso) pelo id ou pelo link.
buscar_bloco_de_rua: o bloco em si (o grupo) pelo nome, com os eventos que o site associa a ele.
Sempre entregue o link do blocosderua.com e cite que os dados são do site.`

// Ferramentas guarda o cliente e o relógio (trocável nos testes).
type Ferramentas struct {
	c     *blocosderua.Cliente
	agora func() time.Time
}

func Novo(c *blocosderua.Cliente) *Ferramentas { return &Ferramentas{c: c, agora: time.Now} }

// Servidor monta um servidor MCP só com estas ferramentas (uso local, por stdio).
func (f *Ferramentas) Servidor() *mcp.Server {
	s := mcp.NewServer(&mcp.Implementation{Name: "blocos-de-rua", Version: "0.1.0"}, &mcp.ServerOptions{Instructions: Instrucoes})
	f.Registrar(s)
	return s
}

func leitura(nome, titulo, descricao string) *mcp.Tool {
	sim := true
	return &mcp.Tool{Name: nome, Description: descricao,
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, OpenWorldHint: &sim, Title: titulo}}
}

// Registrar acrescenta as ferramentas a um servidor MCP existente.
func (f *Ferramentas) Registrar(s *mcp.Server) {
	mcp.AddTool(s, leitura("agenda_blocos_de_rua", "Agenda de blocos de rua",
		"Próximos eventos do blocosderua.com (desfiles de blocos, shows, festas) numa cidade, com data, hora, bairro, endereço, preço e link. Filtros: de/ate (AAAA-MM-DD, hoje, amanha, fim_de_semana), bairro (um ou vários, sem diferença de acento), perto_de (lat,lng) com raio_km, e texto no título ou na descrição."),
		f.Agenda)
	mcp.AddTool(s, leitura("detalhe_bloco_de_rua", "Detalhe de um evento de bloco de rua",
		"Tudo de um evento do blocosderua.com pelo id (da agenda) ou pelo link: descrição, endereço completo, coordenadas, preço, link de ingresso e imagem."),
		f.Detalhe)
	mcp.AddTool(s, leitura("buscar_bloco_de_rua", "Buscar bloco de rua",
		"Procura blocos (o grupo, não o desfile) pelo nome no cadastro do blocosderua.com, com a descrição e os eventos que o site associa a cada um (até 5 com eventos)."),
		f.BuscarBloco)
}

// ---------- agenda ----------

type EntradaAgenda struct {
	Cidade  string   `json:"cidade,omitempty" jsonschema:"sao-paulo (padrão), rio-de-janeiro, belo-horizonte, salvador, florianopolis, recife-olinda, brasilia, porto-alegre ou fortaleza (aceita o nome)"`
	De      string   `json:"de,omitempty" jsonschema:"primeiro dia (AAAA-MM-DD, hoje, amanha ou fim_de_semana); vazio = hoje"`
	Ate     string   `json:"ate,omitempty" jsonschema:"último dia, inclusive (AAAA-MM-DD, hoje, amanha ou fim_de_semana); vazio = sem limite"`
	Bairros []string `json:"bairros,omitempty" jsonschema:"bairros aceitos (trecho do nome, sem diferença de acento), como Pinheiros ou Vila Madalena"`
	PertoDe string   `json:"perto_de,omitempty" jsonschema:"lat,lng do ponto de referência, para filtrar por distância"`
	RaioKm  float64  `json:"raio_km,omitempty" jsonschema:"raio em km a partir de perto_de (padrão 3)"`
	Texto   string   `json:"texto,omitempty" jsonschema:"trecho do título ou da descrição, como samba ou infantil"`
	Limite  int      `json:"limite,omitempty" jsonschema:"máximo de eventos (padrão 30, máximo 200)"`
}

// ItemAgenda é o evento resumido (sem descrição), para a lista caber na resposta.
type ItemAgenda struct {
	ID          int     `json:"id"`
	Titulo      string  `json:"titulo"`
	Data        string  `json:"data"`
	DiaSemana   string  `json:"dia_da_semana"`
	Hora        string  `json:"hora,omitempty"`
	Bairro      string  `json:"bairro,omitempty"`
	Endereco    string  `json:"endereco,omitempty"`
	Preco       string  `json:"preco,omitempty"`
	DistanciaKm float64 `json:"distancia_km,omitempty"`
	Link        string  `json:"link"`
}

type SaidaAgenda struct {
	Cidade    string       `json:"cidade"`
	De        string       `json:"de"`
	Ate       string       `json:"ate,omitempty"`
	Total     int          `json:"total"`
	Mostrados int          `json:"mostrados"`
	Eventos   []ItemAgenda `json:"eventos"`
	Bairros   []string     `json:"bairros_com_eventos_no_periodo,omitempty"`
	Obs       string       `json:"obs,omitempty"`
	Fonte     string       `json:"fonte"`
}

var diasDaSemana = []string{"domingo", "segunda", "terça", "quarta", "quinta", "sexta", "sábado"}

var fuso = func() *time.Location {
	l, err := time.LoadLocation("America/Sao_Paulo")
	if err != nil {
		return time.FixedZone("BRT", -3*3600)
	}
	return l
}()

// LerDia interpreta AAAA-MM-DD, DD/MM[/AAAA], hoje, amanha e fim_de_semana. Devolve o primeiro e
// o último dia do intervalo (iguais, salvo no fim de semana).
func LerDia(s string, agora time.Time) (time.Time, time.Time, error) {
	hoje := time.Date(agora.In(fuso).Year(), agora.In(fuso).Month(), agora.In(fuso).Day(), 0, 0, 0, 0, fuso)
	switch blocosderua.Normalizar(s) {
	case "", "hoje":
		return hoje, hoje, nil
	case "amanha":
		d := hoje.AddDate(0, 0, 1)
		return d, d, nil
	case "fim de semana", "fds":
		sab := hoje.AddDate(0, 0, (int(time.Saturday)-int(hoje.Weekday())+7)%7)
		if hoje.Weekday() == time.Sunday {
			sab = hoje.AddDate(0, 0, -1)
		}
		return sab, sab.AddDate(0, 0, 1), nil
	}
	for _, l := range []string{"2006-01-02", "02/01/2006", "20060102"} {
		if t, err := time.ParseInLocation(l, s, fuso); err == nil {
			return t, t, nil
		}
	}
	if t, err := time.ParseInLocation("02/01", s, fuso); err == nil {
		t = time.Date(hoje.Year(), t.Month(), t.Day(), 0, 0, 0, 0, fuso)
		if t.Before(hoje.AddDate(0, -1, 0)) {
			t = t.AddDate(1, 0, 0)
		}
		return t, t, nil
	}
	return time.Time{}, time.Time{}, fmt.Errorf("data %q: use AAAA-MM-DD, DD/MM, hoje, amanha ou fim_de_semana", s)
}

func latlng(s string) (float64, float64, bool) {
	p := strings.Split(s, ",")
	if len(p) != 2 {
		return 0, 0, false
	}
	lat, err1 := strconv.ParseFloat(strings.TrimSpace(p[0]), 64)
	lng, err2 := strconv.ParseFloat(strings.TrimSpace(p[1]), 64)
	return lat, lng, err1 == nil && err2 == nil && math.Abs(lat) <= 90 && math.Abs(lng) <= 180
}

// Haversine dá a distância em km entre dois pontos.
func Haversine(lat1, lng1, lat2, lng2 float64) float64 {
	const r = 6371.0
	rad := math.Pi / 180
	dLat, dLng := (lat2-lat1)*rad, (lng2-lng1)*rad
	a := math.Sin(dLat/2)*math.Sin(dLat/2) + math.Cos(lat1*rad)*math.Cos(lat2*rad)*math.Sin(dLng/2)*math.Sin(dLng/2)
	return 2 * r * math.Asin(math.Sqrt(a))
}

func arredondar(x float64) float64 { return math.Round(x*10) / 10 }

func (f *Ferramentas) Agenda(ctx context.Context, _ *mcp.CallToolRequest, e EntradaAgenda) (*mcp.CallToolResult, SaidaAgenda, error) {
	cid, err := blocosderua.AcharCidade(e.Cidade)
	if err != nil {
		return nil, SaidaAgenda{}, err
	}
	agora := f.agora()
	de, ateDe, err := LerDia(e.De, agora)
	if err != nil {
		return nil, SaidaAgenda{}, err
	}
	var ate time.Time
	switch {
	case e.Ate != "":
		_, ate, err = LerDia(e.Ate, agora)
		if err != nil {
			return nil, SaidaAgenda{}, err
		}
	case blocosderua.Normalizar(e.De) == "fim de semana" || blocosderua.Normalizar(e.De) == "fds":
		ate = ateDe
	}
	if !ate.IsZero() && ate.Before(de) {
		return nil, SaidaAgenda{}, errors.New("ate é antes de de")
	}
	var refLat, refLng float64
	temRef := false
	if e.PertoDe != "" {
		if refLat, refLng, temRef = latlng(e.PertoDe); !temRef {
			return nil, SaidaAgenda{}, fmt.Errorf("perto_de %q: use lat,lng (ex.: -23.5614,-46.6559)", e.PertoDe)
		}
	}
	raio := e.RaioKm
	if raio <= 0 {
		raio = 3
	}
	limite := e.Limite
	if limite <= 0 {
		limite = 30
	}
	if limite > 200 {
		limite = 200
	}
	eventos, err := f.c.Agenda(ctx, cid)
	if err != nil {
		return nil, SaidaAgenda{}, err
	}
	saida := SaidaAgenda{Cidade: cid.Nome, De: de.Format("2006-01-02"), Fonte: "blocosderua.com", Eventos: []ItemAgenda{}}
	if !ate.IsZero() {
		saida.Ate = ate.Format("2006-01-02")
	}
	texto := blocosderua.Normalizar(e.Texto)
	bairrosVistos := map[string]bool{}
	for _, ev := range eventos {
		ini := ev.Inicio()
		dia := time.Date(ini.Year(), ini.Month(), ini.Day(), 0, 0, 0, 0, fuso)
		if dia.Before(de) || (!ate.IsZero() && dia.After(ate)) {
			continue
		}
		if ev.Bairro != "" {
			bairrosVistos[ev.Bairro] = true
		}
		if len(e.Bairros) > 0 && !algumBairro(ev.Bairro, e.Bairros) {
			continue
		}
		if texto != "" && !strings.Contains(blocosderua.Normalizar(ev.Titulo+" "+ev.Descricao), texto) {
			continue
		}
		item := ItemAgenda{ID: ev.ID, Titulo: ev.Titulo, Data: ev.Data, DiaSemana: diasDaSemana[ini.Weekday()],
			Hora: ev.Hora, Bairro: ev.Bairro, Endereco: ev.Endereco, Preco: ev.Preco, Link: ev.Link}
		if temRef {
			if ev.Lat == 0 && ev.Lng == 0 {
				continue
			}
			d := Haversine(refLat, refLng, ev.Lat, ev.Lng)
			if d > raio {
				continue
			}
			item.DistanciaKm = arredondar(d)
		}
		saida.Total++
		if len(saida.Eventos) < limite {
			saida.Eventos = append(saida.Eventos, item)
		}
	}
	saida.Mostrados = len(saida.Eventos)
	if saida.Total == 0 && len(e.Bairros) > 0 {
		for b := range bairrosVistos {
			saida.Bairros = append(saida.Bairros, b)
		}
		ordenar(saida.Bairros)
	}
	switch {
	case len(eventos) == 0:
		saida.Obs = "o site não tem nenhum evento futuro publicado para esta cidade agora"
	case saida.Total > saida.Mostrados:
		saida.Obs = fmt.Sprintf("há mais %d eventos; aumente limite ou estreite os filtros", saida.Total-saida.Mostrados)
	}
	return nil, saida, nil
}

func algumBairro(bairro string, aceitos []string) bool {
	b := blocosderua.Normalizar(bairro)
	for _, a := range aceitos {
		if n := blocosderua.Normalizar(a); n != "" && strings.Contains(b, n) {
			return true
		}
	}
	return false
}

func ordenar(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && blocosderua.Normalizar(s[j]) < blocosderua.Normalizar(s[j-1]); j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}

// ---------- detalhe ----------

type EntradaDetalhe struct {
	ID     int    `json:"id,omitempty" jsonschema:"id do evento (da agenda_blocos_de_rua)"`
	Link   string `json:"link,omitempty" jsonschema:"link do evento no blocosderua.com, no lugar do id"`
	Cidade string `json:"cidade,omitempty" jsonschema:"cidade do evento (padrão sao-paulo; com link, sai dele)"`
}

type SaidaDetalhe struct {
	blocosderua.Evento
	DiaSemana string `json:"dia_da_semana"`
	Fonte     string `json:"fonte"`
}

// slugDoLink tira a cidade e o slug de https://www.blocosderua.com/[cidade/]programacao/<slug>/.
func slugDoLink(link string) (cidade, slug string, ok bool) {
	i := strings.Index(link, "blocosderua.com/")
	if i < 0 {
		return "", "", false
	}
	partes := strings.Split(strings.Trim(strings.SplitN(link[i+len("blocosderua.com/"):], "?", 2)[0], "/"), "/")
	for k, p := range partes {
		if p == "programacao" && k+1 < len(partes) {
			if k > 0 {
				cidade = partes[k-1]
			}
			return cidade, partes[k+1], true
		}
	}
	return "", "", false
}

func (f *Ferramentas) Detalhe(ctx context.Context, _ *mcp.CallToolRequest, e EntradaDetalhe) (*mcp.CallToolResult, SaidaDetalhe, error) {
	var ev blocosderua.Evento
	switch {
	case e.Link != "":
		cidade, slug, ok := slugDoLink(e.Link)
		if !ok {
			return nil, SaidaDetalhe{}, fmt.Errorf("link %q não é de um evento do blocosderua.com (…/programacao/<nome>/)", e.Link)
		}
		cid, err := blocosderua.AcharCidade(cidade)
		if err != nil {
			return nil, SaidaDetalhe{}, err
		}
		if ev, err = f.c.EventoPeloSlug(ctx, cid, slug); err != nil {
			return nil, SaidaDetalhe{}, erroDeBusca(err, "evento "+slug)
		}
	case e.ID > 0:
		cid, err := blocosderua.AcharCidade(e.Cidade)
		if err != nil {
			return nil, SaidaDetalhe{}, err
		}
		if ev, err = f.c.Evento(ctx, cid, e.ID); err != nil {
			return nil, SaidaDetalhe{}, erroDeBusca(err, fmt.Sprintf("evento %d em %s", e.ID, cid.Nome))
		}
	default:
		return nil, SaidaDetalhe{}, errors.New("informe id ou link")
	}
	return nil, SaidaDetalhe{Evento: ev, DiaSemana: diasDaSemana[ev.Inicio().Weekday()], Fonte: "blocosderua.com"}, nil
}

func erroDeBusca(err error, oque string) error {
	if errors.Is(err, blocosderua.ErrNaoEncontrado) {
		return fmt.Errorf("%s não encontrado (já passou ou é de outra cidade?)", oque)
	}
	return err
}

// ---------- blocos ----------

type EntradaBloco struct {
	Nome   string `json:"nome" jsonschema:"nome do bloco ou trecho dele, como Acadêmicos do Baixo Augusta"`
	Cidade string `json:"cidade,omitempty" jsonschema:"cidade (padrão sao-paulo)"`
}

type SaidaBloco struct {
	Cidade      string              `json:"cidade"`
	Encontrados int                 `json:"encontrados"`
	Blocos      []blocosderua.Bloco `json:"blocos"`
	Obs         string              `json:"obs,omitempty"`
	Fonte       string              `json:"fonte"`
}

const maxBlocosComEventos = 5

func (f *Ferramentas) BuscarBloco(ctx context.Context, _ *mcp.CallToolRequest, e EntradaBloco) (*mcp.CallToolResult, SaidaBloco, error) {
	n := blocosderua.Normalizar(e.Nome)
	if n == "" {
		return nil, SaidaBloco{}, errors.New("informe o nome do bloco")
	}
	cid, err := blocosderua.AcharCidade(e.Cidade)
	if err != nil {
		return nil, SaidaBloco{}, err
	}
	todos, err := f.c.Blocos(ctx, cid)
	if err != nil {
		return nil, SaidaBloco{}, err
	}
	var exatos, parciais []blocosderua.Bloco
	for _, b := range todos {
		nb := blocosderua.Normalizar(b.Nome)
		switch {
		case nb == n:
			exatos = append(exatos, b)
		case strings.Contains(nb, n):
			parciais = append(parciais, b)
		}
	}
	achados := append(exatos, parciais...)
	saida := SaidaBloco{Cidade: cid.Nome, Encontrados: len(achados), Blocos: []blocosderua.Bloco{}, Fonte: "blocosderua.com"}
	for i, b := range achados {
		if i >= 20 {
			saida.Obs = fmt.Sprintf("mostrando 20 de %d; use um nome mais completo", len(achados))
			break
		}
		if i < maxBlocosComEventos {
			if completo, err := f.c.Bloco(ctx, cid, b.ID); err == nil {
				completo.Link = b.Link
				b = completo
			}
		}
		saida.Blocos = append(saida.Blocos, b)
	}
	if len(achados) == 0 {
		saida.Obs = "nenhum bloco com esse nome; tente um trecho menor ou agenda_blocos_de_rua com texto"
	}
	return nil, saida, nil
}
