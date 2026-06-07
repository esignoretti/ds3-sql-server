package api

import (
	"html/template"
	"net/http"

	"github.com/esignoretti/ds3-sql-server/internal/catalog"
	"github.com/esignoretti/ds3-sql-server/internal/metastore"
)

// CatalogFragmentHandler renders an HTML fragment of the dataset/table tree for
// the Web UI catalog browser. It is the testable, progressive-enhancement
// rendering path; catalog.js consumes the JSON /datasets endpoints for the live
// interactive tree.
type CatalogFragmentHandler struct {
	cat  *catalog.Service
	tmpl *template.Template
}

type catalogTreeData struct {
	Datasets []datasetNode
}

type datasetNode struct {
	Name   string
	Tables []*metastore.Table
}

const catalogTreeTmpl = `{{if not .Datasets}}<p class="catalog-empty">No datasets. Create one to get started.</p>{{else}}<ul class="catalog-tree">{{range .Datasets}}<li class="catalog-ds"><div class="catalog-ds-name" data-dataset="{{.Name}}">{{.Name}}</div><ul class="catalog-tables">{{range .Tables}}<li class="catalog-table" data-dataset="{{.Dataset}}" data-table="{{.Name}}" onclick="selectCatalogTable('{{.Dataset}}','{{.Name}}')"><span class="catalog-table-name">{{.Name}}</span> <span class="catalog-table-meta">{{.Format}} · {{.Stats.RowCount}} rows</span></li>{{else}}<li class="catalog-table-empty">no tables</li>{{end}}</ul></li>{{end}}</ul>{{end}}`

func NewCatalogFragmentHandler(cat *catalog.Service) *CatalogFragmentHandler {
	return &CatalogFragmentHandler{
		cat:  cat,
		tmpl: template.Must(template.New("catalogTree").Parse(catalogTreeTmpl)),
	}
}

func (h *CatalogFragmentHandler) TreeForProject(w http.ResponseWriter, r *http.Request, projectID string) {
	ctx := r.Context()
	datasets, err := h.cat.ListDatasets(ctx, projectID)
	if err != nil {
		http.Error(w, `{"error":"`+err.Error()+`"}`, http.StatusInternalServerError)
		return
	}
	data := catalogTreeData{}
	for _, ds := range datasets {
		tables, err := h.cat.ListTables(ctx, projectID, ds.Name)
		if err != nil {
			http.Error(w, `{"error":"`+err.Error()+`"}`, http.StatusInternalServerError)
			return
		}
		data.Datasets = append(data.Datasets, datasetNode{Name: ds.Name, Tables: tables})
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := h.tmpl.Execute(w, data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}
