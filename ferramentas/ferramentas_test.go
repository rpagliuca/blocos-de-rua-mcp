package ferramentas

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/rpagliuca/blocos-de-rua-mcp/blocosderua"
)

// segunda, 23/11/2026 10:00 em São Paulo
var agoraTeste = time.Date(2026, 11, 23, 10, 0, 0, 0, fuso)

func ev(id int, data, bairro string, lat, lng float64, titulo string) string {
	return fmt.Sprintf(`{"id":%d,"link":"https://www.blocosderua.com/programacao/e-%d/","title":{"rendered":%q},"content":{"rendered":"<p>desc %d</p>"},
	"acf":{"data":%q,"hora":"14:00","endereco":"Rua %d","geolocalizacao":{"address":"x","lat":%v,"lng":%v},"preco":"Grátis","bairro":%q}}`,
		id, id, titulo, id, data, id, lat, lng, bairro)
}

func novo(t *testing.T) *Ferramentas {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/wp-json/wp/v2/programacao":
			if r.URL.Query().Get("slug") == "e-2" {
				fmt.Fprint(w, "["+ev(2, "20261128", "Pinheiros", -23.56, -46.69, "Samba no Largo")+"]")
				return
			}
			w.Header().Set("X-WP-TotalPages", "1")
			fmt.Fprint(w, "["+strings.Join([]string{
				ev(1, "20261123", "Água Branca", -23.527, -46.669, "Show de hoje"),
				ev(2, "20261128", "Pinheiros", -23.56, -46.69, "Samba no Largo"),
				ev(3, "20261129", "Vila Madalena", -23.553, -46.689, "Bloco infantil"),
				ev(4, "20261206", "Mooca", -23.55, -46.59, "Reggae"),
				ev(5, "20261120", "Sé", -23.55, -46.63, "Já passou (cache do site)"),
			}, ",")+"]")
		case "/wp-json/wp/v2/programacao/4":
			fmt.Fprint(w, ev(4, "20261206", "Mooca", -23.55, -46.59, "Reggae"))
		case "/salvador/wp-json/wp/v2/programacao":
			w.Header().Set("X-WP-TotalPages", "0")
			fmt.Fprint(w, "[]")
		case "/wp-json/site/v1/blocos":
			fmt.Fprint(w, `[{"ID":1,"post_title":"Bloco do Samba","post_name":"bloco-do-samba"},{"ID":2,"post_title":"Samba","post_name":"samba"},{"ID":3,"post_title":"Outro","post_name":"outro"}]`)
		case "/wp-json/mobile/blocos/1", "/wp-json/mobile/blocos/2":
			id := strings.TrimPrefix(r.URL.Path, "/wp-json/mobile/blocos/")
			fmt.Fprintf(w, `{"ID":%q,"post_title":"x","post_name":"x","programacao":[{"ID":"9","post_title":"Desfile","acf":{"data":"20270207"}}]}`, id)
		default:
			t.Errorf("rota inesperada %s", r.URL)
			w.WriteHeader(404)
		}
	}))
	t.Cleanup(s.Close)
	c := blocosderua.Novo()
	c.Base = s.URL
	f := Novo(c)
	f.agora = func() time.Time { return agoraTeste }
	return f
}

func ids(s SaidaAgenda) []int {
	var r []int
	for _, e := range s.Eventos {
		r = append(r, e.ID)
	}
	return r
}

func TestAgendaFiltros(t *testing.T) {
	f := novo(t)
	ctx := context.Background()
	casos := []struct {
		nome string
		e    EntradaAgenda
		quer string
	}{
		{"padrão: de hoje em diante, sem o que já passou", EntradaAgenda{}, "[1 2 3 4]"},
		{"fim de semana", EntradaAgenda{De: "fim_de_semana"}, "[2 3]"},
		{"intervalo", EntradaAgenda{De: "2026-11-24", Ate: "28/11"}, "[2]"},
		{"bairro sem acento", EntradaAgenda{Bairros: []string{"agua branca", "MOOCA"}}, "[1 4]"},
		{"texto", EntradaAgenda{Texto: "infantil"}, "[3]"},
		{"perto de Pinheiros, 2 km", EntradaAgenda{PertoDe: "-23.561,-46.690", RaioKm: 2}, "[2 3]"},
		{"limite", EntradaAgenda{Limite: 2}, "[1 2]"},
	}
	for _, c := range casos {
		_, s, err := f.Agenda(ctx, nil, c.e)
		if err != nil {
			t.Fatalf("%s: %v", c.nome, err)
		}
		if got := fmt.Sprint(ids(s)); got != c.quer {
			t.Errorf("%s: %s, quer %s", c.nome, got, c.quer)
		}
	}
	_, s, _ := f.Agenda(ctx, nil, EntradaAgenda{Limite: 2})
	if s.Total != 4 || !strings.Contains(s.Obs, "mais 2") {
		t.Errorf("limite: total %d obs %q", s.Total, s.Obs)
	}
	_, s, _ = f.Agenda(ctx, nil, EntradaAgenda{PertoDe: "-23.561,-46.690", RaioKm: 2})
	if s.Eventos[0].DistanciaKm == 0 || s.Eventos[0].DiaSemana != "sábado" {
		t.Errorf("distância/dia: %+v", s.Eventos[0])
	}
	_, s, _ = f.Agenda(ctx, nil, EntradaAgenda{Bairros: []string{"Lapa"}})
	if s.Total != 0 || len(s.Bairros) != 4 || s.Bairros[0] != "Água Branca" {
		t.Errorf("sugestão de bairros: %+v", s.Bairros)
	}
	_, s, _ = f.Agenda(ctx, nil, EntradaAgenda{Cidade: "Salvador"})
	if s.Cidade != "Salvador" || !strings.Contains(s.Obs, "nenhum evento") || s.Eventos == nil {
		t.Errorf("cidade vazia: %+v", s)
	}
	for _, e := range []EntradaAgenda{{De: "ontem?"}, {PertoDe: "Pinheiros"}, {Cidade: "Curitiba"}, {De: "2026-12-01", Ate: "2026-11-30"}} {
		if _, _, err := f.Agenda(ctx, nil, e); err == nil {
			t.Errorf("esperava erro para %+v", e)
		}
	}
}

func TestLerDia(t *testing.T) {
	dom := time.Date(2026, 11, 29, 9, 0, 0, 0, fuso)
	a, b, _ := LerDia("fim_de_semana", dom)
	if a.Day() != 28 || b.Day() != 29 {
		t.Errorf("domingo: %v %v", a, b)
	}
	a, _, _ = LerDia("05/01", agoraTeste)
	if a.Year() != 2027 {
		t.Errorf("05/01 deveria ser 2027: %v", a)
	}
	a, _, _ = LerDia("amanha", agoraTeste)
	if a.Day() != 24 {
		t.Errorf("amanhã: %v", a)
	}
}

func TestDetalhe(t *testing.T) {
	f := novo(t)
	ctx := context.Background()
	_, s, err := f.Detalhe(ctx, nil, EntradaDetalhe{ID: 4})
	if err != nil || s.Titulo != "Reggae" || s.Descricao != "desc 4" || s.DiaSemana != "domingo" {
		t.Fatalf("%+v %v", s, err)
	}
	_, s, err = f.Detalhe(ctx, nil, EntradaDetalhe{Link: "https://www.blocosderua.com/programacao/e-2/?utm=x"})
	if err != nil || s.ID != 2 {
		t.Fatalf("%+v %v", s, err)
	}
	if _, _, err := f.Detalhe(ctx, nil, EntradaDetalhe{}); err == nil {
		t.Error("sem id nem link deveria falhar")
	}
	c, slug, ok := slugDoLink("https://www.blocosderua.com/rio-de-janeiro/programacao/show-rj-01-12-26/")
	if !ok || c != "rio-de-janeiro" || slug != "show-rj-01-12-26" {
		t.Errorf("slugDoLink: %q %q %v", c, slug, ok)
	}
}

func TestBuscarBloco(t *testing.T) {
	f := novo(t)
	_, s, err := f.BuscarBloco(context.Background(), nil, EntradaBloco{Nome: "samba"})
	if err != nil || s.Encontrados != 2 || s.Blocos[0].ID != 2 || len(s.Blocos[0].Eventos) != 1 {
		t.Fatalf("%+v %v", s, err)
	}
	if s.Blocos[0].Link != blocosderua.BaseURL+"/blocos/samba/" {
		t.Errorf("link: %s", s.Blocos[0].Link)
	}
	_, s, _ = f.BuscarBloco(context.Background(), nil, EntradaBloco{Nome: "nada disso"})
	if s.Encontrados != 0 || s.Obs == "" {
		t.Errorf("%+v", s)
	}
}

func TestRegistrar(t *testing.T) {
	s := mcp.NewServer(&mcp.Implementation{Name: "t"}, nil)
	novo(t).Registrar(s)
	c := mcp.NewClient(&mcp.Implementation{Name: "c"}, nil)
	ts, tc := mcp.NewInMemoryTransports()
	ctx := context.Background()
	if _, err := s.Connect(ctx, ts, nil); err != nil {
		t.Fatal(err)
	}
	sess, err := c.Connect(ctx, tc, nil)
	if err != nil {
		t.Fatal(err)
	}
	l, err := sess.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	var nomes []string
	for _, x := range l.Tools {
		nomes = append(nomes, x.Name)
	}
	if fmt.Sprint(nomes) != "[agenda_blocos_de_rua buscar_bloco_de_rua detalhe_bloco_de_rua]" {
		t.Errorf("ferramentas: %v", nomes)
	}
}
