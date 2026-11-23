package blocosderua

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func evento(id int, data, hora, bairro string) string {
	return fmt.Sprintf(`{"id":%d,"slug":"e-%d","link":"https://www.blocosderua.com/programacao/e-%d/",
	"title":{"rendered":"Evento &#8211; %d"},"content":{"rendered":"<p>Desfile&nbsp;com <strong>bateria</strong>.</p>\r\n\r\n<span>Siga nosso</span> <a href=\"x\">instagram @blocosderuasp</a>, nosso facebook"},
	"featured_image_src":"https://img/%d.jpg",
	"acf":{"data":"%s","hora":"%s","endereco":"Rua A, 1","geolocalizacao":{"address":"Rua A, 1 - %s","lat":-23.5,"lng":-46.6,"zoom":16},"preco":"Grátis","bairro":"%s","comprar":"","visualizacoes":""}}`,
		id, id, id, id, id, data, hora, bairro, bairro)
}

func falso(t *testing.T, pedidos *atomic.Int32) *Cliente {
	t.Helper()
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		pedidos.Add(1)
		switch {
		case r.URL.Path == "/wp-json/wp/v2/programacao" && r.URL.Query().Get("slug") != "":
			fmt.Fprint(w, "["+evento(7, "20261127", "21:00", "Barra Funda")+"]")
		case r.URL.Path == "/wp-json/wp/v2/programacao":
			if r.URL.Query().Get("per_page") != "100" {
				t.Errorf("per_page = %q", r.URL.Query().Get("per_page"))
			}
			w.Header().Set("X-WP-TotalPages", "2")
			if r.URL.Query().Get("page") == "1" {
				fmt.Fprint(w, "["+evento(2, "20261128", "15:00", "Pinheiros")+","+evento(1, "20261126", "20:00", "Água Branca")+"]")
			} else {
				fmt.Fprint(w, `[{"id":3,"link":"l","title":{"rendered":"Sem campos"},"content":{"rendered":""},"featured_image_src":false,"acf":[]}]`)
			}
		case r.URL.Path == "/rio-de-janeiro/wp-json/wp/v2/programacao/9":
			fmt.Fprint(w, evento(9, "20261211", "", "Lapa"))
		case r.URL.Path == "/wp-json/wp/v2/programacao/404":
			w.WriteHeader(404)
			fmt.Fprint(w, `{"code":"rest_post_invalid_id"}`)
		case r.URL.Path == "/wp-json/site/v1/blocos":
			fmt.Fprint(w, `[{"ID":560,"post_title":"Acadêmicos do Baixo Augusta","post_name":"academicos-do-baixo-augusta","post_content":"Bloco &amp; folia","url":"https://www.blocosderua.com/?p=560","acf":false}]`)
		case r.URL.Path == "/wp-json/mobile/blocos/560":
			fmt.Fprint(w, `{"ID":"560","post_title":"Acadêmicos do Baixo Augusta","post_name":"academicos-do-baixo-augusta","post_content":"Bloco","url":"u","acf":false,
			"programacao":[{"ID":"77","post_title":"Desfile","post_content":"","url":"https://www.blocosderua.com/programacao/desfile/","acf":{"data":"20270207","hora":"10:00","bairro":"Consolação"}}]}`)
		case r.URL.Path == "/wp-json/wp/v2/erro":
			w.WriteHeader(500)
			fmt.Fprint(w, "<html>Erro crítico</html>")
		default:
			t.Errorf("rota inesperada %s", r.URL)
			w.WriteHeader(404)
		}
	}))
	t.Cleanup(s.Close)
	c := Novo()
	c.Base = s.URL
	return c
}

func TestAgendaPaginaOrdenaENormaliza(t *testing.T) {
	var n atomic.Int32
	c := falso(t, &n)
	sp, _ := AcharCidade("")
	es, err := c.Agenda(context.Background(), sp)
	if err != nil {
		t.Fatal(err)
	}
	if len(es) != 3 || n.Load() != 2 {
		t.Fatalf("eventos=%d pedidos=%d", len(es), n.Load())
	}
	// "Sem campos" não tem data e vai para o começo; os outros em ordem de data.
	if es[1].ID != 1 || es[2].ID != 2 {
		t.Fatalf("ordem: %d %d", es[1].ID, es[2].ID)
	}
	e := es[1]
	if e.Data != "2026-11-26" || e.Hora != "20:00" || e.Bairro != "Água Branca" || e.Titulo != "Evento – 1" || e.Lat != -23.5 {
		t.Fatalf("evento: %+v", e)
	}
	if e.Descricao != "Desfile com bateria." {
		t.Fatalf("descrição: %q", e.Descricao)
	}
	if es[0].Imagem != "" || es[0].Bairro != "" {
		t.Fatalf("evento sem acf: %+v", es[0])
	}
	// segunda chamada vem do cache
	if _, err := c.Agenda(context.Background(), sp); err != nil || n.Load() != 2 {
		t.Fatalf("cache: pedidos=%d err=%v", n.Load(), err)
	}
	c.agora = func() time.Time { return time.Now().Add(time.Hour) }
	if _, _ = c.Agenda(context.Background(), sp); n.Load() != 4 {
		t.Fatalf("cache vencido não refez: %d", n.Load())
	}
}

func TestEventoNaCidadeENaoEncontrado(t *testing.T) {
	var n atomic.Int32
	c := falso(t, &n)
	rio, _ := AcharCidade("Rio de Janeiro")
	e, err := c.Evento(context.Background(), rio, 9)
	if err != nil || e.Cidade != "rio-de-janeiro" || e.Data != "2026-12-11" || e.Inicio().Hour() != 0 {
		t.Fatalf("%+v %v", e, err)
	}
	sp, _ := AcharCidade("sao-paulo")
	if _, err := c.Evento(context.Background(), sp, 404); !errors.Is(err, ErrNaoEncontrado) {
		t.Fatalf("esperava não encontrado: %v", err)
	}
	e, err = c.EventoPeloSlug(context.Background(), sp, "e-7")
	if err != nil || e.ID != 7 || e.Ingressos != "" {
		t.Fatalf("%+v %v", e, err)
	}
	var x []int
	if _, err := c.get(context.Background(), sp, "wp/v2/erro", nil, &x); err == nil || !strings.Contains(err.Error(), "HTTP 500") {
		t.Fatalf("erro 500: %v", err)
	}
}

func TestBlocos(t *testing.T) {
	var n atomic.Int32
	c := falso(t, &n)
	sp, _ := AcharCidade("SP")
	bs, err := c.Blocos(context.Background(), sp)
	if err != nil || len(bs) != 1 || bs[0].Descricao != "Bloco & folia" || bs[0].Link != BaseURL+"/blocos/academicos-do-baixo-augusta/" {
		t.Fatalf("%+v %v", bs, err)
	}
	b, err := c.Bloco(context.Background(), sp, 560)
	if err != nil || len(b.Eventos) != 1 || b.Eventos[0].Data != "2027-02-07" || b.Eventos[0].Bairro != "Consolação" {
		t.Fatalf("%+v %v", b, err)
	}
}

func TestAcharCidade(t *testing.T) {
	for entrada, codigo := range map[string]string{"": "sao-paulo", "São Paulo": "sao-paulo", "sp": "sao-paulo",
		"florianópolis": "florianopolis", "Olinda": "recife-olinda", "brasilia": "brasilia", "porto-alegre": "porto-alegre"} {
		c, err := AcharCidade(entrada)
		if err != nil || c.Codigo != codigo {
			t.Errorf("%q → %q, %v", entrada, c.Codigo, err)
		}
	}
	if _, err := AcharCidade("Curitiba"); err == nil {
		t.Error("Curitiba deveria falhar")
	}
}
