// Comando blocos-de-rua-mcp: servidor MCP (stdio) e CLI de teste da agenda do blocosderua.com.
//
//	blocos-de-rua-mcp                                  servidor MCP por stdio
//	blocos-de-rua-mcp agenda [cidade] [de] [ate]
//	blocos-de-rua-mcp detalhe ID|LINK [cidade]
//	blocos-de-rua-mcp bloco NOME [cidade]
//	blocos-de-rua-mcp json FERRAMENTA '{"campo": ...}'   qualquer ferramenta, com a entrada em JSON
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/rpagliuca/blocos-de-rua-mcp/blocosderua"
	"github.com/rpagliuca/blocos-de-rua-mcp/ferramentas"
)

func arg(a []string, i int) string {
	if i < len(a) {
		return a[i]
	}
	return ""
}

func main() {
	f := ferramentas.Novo(blocosderua.Novo())
	ctx := context.Background()
	a := os.Args[1:]
	var saida any
	var err error
	switch {
	case len(a) == 0:
		if err := f.Servidor().Run(ctx, &mcp.StdioTransport{}); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	case a[0] == "agenda":
		_, saida, err = f.Agenda(ctx, nil, ferramentas.EntradaAgenda{Cidade: arg(a, 1), De: arg(a, 2), Ate: arg(a, 3)})
	case a[0] == "detalhe" && len(a) >= 2:
		e := ferramentas.EntradaDetalhe{Cidade: arg(a, 2)}
		if id, erro := strconv.Atoi(a[1]); erro == nil {
			e.ID = id
		} else {
			e.Link = a[1]
		}
		_, saida, err = f.Detalhe(ctx, nil, e)
	case a[0] == "bloco" && len(a) >= 2:
		_, saida, err = f.BuscarBloco(ctx, nil, ferramentas.EntradaBloco{Nome: a[1], Cidade: arg(a, 2)})
	case a[0] == "json" && len(a) == 3:
		saida, err = porJSON(ctx, f, a[1], a[2])
	default:
		fmt.Fprintln(os.Stderr, "uso: blocos-de-rua-mcp [agenda [cidade] [de] [ate] | detalhe ID|LINK [cidade] | bloco NOME [cidade] | json FERRAMENTA JSON]")
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	_ = enc.Encode(saida)
}

func porJSON(ctx context.Context, f *ferramentas.Ferramentas, nome, entrada string) (any, error) {
	dec := func(v any) error {
		d := json.NewDecoder(strings.NewReader(entrada))
		d.DisallowUnknownFields()
		return d.Decode(v)
	}
	switch nome {
	case "agenda_blocos_de_rua":
		var e ferramentas.EntradaAgenda
		if err := dec(&e); err != nil {
			return nil, err
		}
		_, s, err := f.Agenda(ctx, nil, e)
		return s, err
	case "detalhe_bloco_de_rua":
		var e ferramentas.EntradaDetalhe
		if err := dec(&e); err != nil {
			return nil, err
		}
		_, s, err := f.Detalhe(ctx, nil, e)
		return s, err
	case "buscar_bloco_de_rua":
		var e ferramentas.EntradaBloco
		if err := dec(&e); err != nil {
			return nil, err
		}
		_, s, err := f.BuscarBloco(ctx, nil, e)
		return s, err
	}
	return nil, fmt.Errorf("ferramenta %q desconhecida", nome)
}
