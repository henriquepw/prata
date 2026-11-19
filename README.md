# Prata

Monorepo Go com dois módulos: `server/` para o servidor e a web, e `tui/` para o cliente de terminal. O `go.work` da raiz inclui os dois módulos; a documentação do projeto fica em `../docs`.

```sh
go run ./server/cmd/server
go run ./tui/cmd/tui
```

## Web

Os arquivos gerados da web (`server/internal/web/**/*_templ.go` e `server/internal/web/static/app.css`) são commitados, então `go build` sozinho gera o binário. Depois de mudar um `.templ` ou classes Tailwind, regenere com o [Tailwind v4 standalone](https://github.com/tailwindlabs/tailwindcss/releases) no `PATH`:

```sh
cd server/internal/web && go generate
```

Isso roda `go tool templ generate` e `tailwindcss -i static/globals.css -o static/app.css --minify`. No dev, `tailwindcss -i static/globals.css -o static/app.css --watch` no mesmo diretório.
