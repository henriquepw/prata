# Exemplos

| Exemplo | O que mostra |
| ------- | ------------ |
| [`simple/`](simple/compose.yaml) | Mínimo para subir: imagem, porta e volume nomeado. |
| [`full/`](full/compose.yaml) | HTTPS com Caddy, `.env`, healthcheck, bind mount, limite de memória e rotação de logs. |

```sh
cd examples/simple   # ou examples/full
docker compose up -d
```

## Variáveis de ambiente

| Variável | Padrão | Descrição |
| -------- | ------ | --------- |
| `PRATA_DB_PATH` | `/data/prata.db` | Caminho do banco SQLite. |
| `PRATA_ADDR` | `:8080` | Endereço em que o servidor escuta. |
| `PRATA_ALLOW_SIGNUP` | `false` | `true` permite criar contas. |
| `PRATA_ENCRYPTION_KEY` | vazio | Habilita o Open Finance; cifra as credenciais da Pluggy. |
| `PRATA_PLUGGY_URL` | `https://api.pluggy.ai` | Endereço da API da Pluggy. |

O tempo de vida das sessões não é configurável.
