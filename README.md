# Prata

Monorepo Go com dois módulos: `server/` para o servidor e a web, e `tui/` para o cliente de terminal. O `go.work` da raiz inclui os dois módulos; a documentação do projeto fica em `../docs`.

```sh
go run ./server/cmd/server
go run ./tui/cmd/tui
```

No dev, rode `make setup` uma vez (instala `air` e o Tailwind standalone em `.bin/`) e depois `make start/server` ou `make start/tui` para hot reload. `make help` lista os comandos (`build`, `test` e variantes por módulo).

## Web

Os arquivos gerados da web (`server/internal/web/**/*_templ.go` e `server/internal/web/static/app.css`) são commitados, então `go build` sozinho gera o binário. Depois de mudar um `.templ` ou classes Tailwind, regenere com o [Tailwind v4 standalone](https://github.com/tailwindlabs/tailwindcss/releases) no `PATH`:

```sh
cd server/internal/web && go generate
```

Isso roda `go tool templ generate -path ../..` (a partir de `server`, para manter os caminhos dos `*_templ.go` commitados) e `tailwindcss -i static/globals.css -o static/app.css --minify`. No dev, `tailwindcss -i static/globals.css -o static/app.css --watch` no mesmo diretório.

## Docker

A imagem `ghcr.io/henriquepw/prata` (amd64/arm64) é publicada a cada tag `v*`. Para subir, use o [`compose.yaml`](compose.yaml) de exemplo:

```sh
docker compose up -d
```

Nenhuma variável é obrigatória: o banco fica em `/data/prata.db` e o servidor escuta em `:8080`. Para permitir cadastro, descomente `PRATA_ALLOW_SIGNUP` no compose. Para atualizar:

```sh
docker compose pull && docker compose up -d
```

- **Open Finance**: defina `PRATA_ENCRYPTION_KEY` com uma chave aleatória forte (gere com `openssl rand -base64 32`) para cada usuário guardar, cifradas, as próprias credenciais da Pluggy em Configurações; sem ela a funcionalidade não aparece. Trocar a chave invalida as credenciais salvas. `PRATA_PLUGGY_URL` muda o endereço da API (padrão `https://api.pluggy.ai`).
- **TLS**: termine o HTTPS num reverse proxy e repasse o header `X-Forwarded-Proto`; sem ele o cookie de sessão não recebe `Secure`.
- **Backup**: pare o container (`docker compose stop`) e copie o volume `/data` inteiro (`prata.db`, `-wal` e `-shm`). Para restaurar, devolva os arquivos com o container parado.
- **Bind mount**: o container roda como UID 65532; se trocar o volume nomeado por um diretório do host, dê a posse a ele (`sudo chown -R 65532:65532 ./data`).
- **Senha esquecida**: `docker exec -it prata prata reset-password <email>` pede a nova senha (digitada ou via pipe no stdin), encerra as sessões web e revoga as API keys do usuário.
