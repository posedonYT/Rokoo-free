# Сохранение данных между запусками

## В чём проблема

Сейчас Postgres в `backend/deploy/docker-compose.yml` **не хранит данные на диске**.

У сервиса `postgres` смонтирован только скрипт инициализации:

```yaml
volumes:
  - ./postgres/init.sql:/docker-entrypoint-initdb.d/init.sql
```

Нет volume на `/var/lib/postgresql/data`. При `docker compose down` контейнер удаляется вместе со всеми БД: аккаунты, серверы, сообщения, медиа-метаданные пропадают. При следующем `up` снова выполняется `init.sql` и поднимаются **пустые** базы.

То же относится к **Redis** (сессии refresh / presence) и **MinIO** (файлы вложений): у них тоже нет persistent volumes.

---

## Что сделать для Postgres (обязательно)

В `backend/deploy/docker-compose.yml` изменить сервис `postgres`:

```yaml
postgres:
  image: postgres:16-alpine
  environment:
    POSTGRES_USER: rokko
    POSTGRES_PASSWORD: rokko
    POSTGRES_DB: postgres
  ports:
    - "5432:5432"
  volumes:
    - postgres_data:/var/lib/postgresql/data
    - ./postgres/init.sql:/docker-entrypoint-initdb.d/init.sql
  healthcheck:
    test: ["CMD-SHELL", "pg_isready -U rokko"]
    interval: 5s
    timeout: 3s
    retries: 20
```

В **конце файла** (на одном уровне с `services:`) добавить:

```yaml
volumes:
  postgres_data:
```

### Важно про `init.sql`

Скрипт в `docker-entrypoint-initdb.d` выполняется **только при первой инициализации** пустого data-каталога (когда volume ещё пустой). После появления volume:

- первый `up` создаст volume, прогонит `init.sql`, создаст БД `auth`, `users`, `guilds`, `messages`, `media`;
- следующие `down` / `up` **сохранят** аккаунты и остальное;
- `init.sql` повторно не запустится (это нормально).

---

## Желательно: Redis и MinIO

Чтобы не терять refresh-токены / presence и загруженные файлы:

```yaml
redis:
  # ...
  volumes:
    - redis_data:/data

minio:
  # ...
  volumes:
    - minio_data:/data
```

И в секцию `volumes:`:

```yaml
volumes:
  postgres_data:
  redis_data:
  minio_data:
```

(путь данных MinIO зависит от того, как настроен контейнер в `deploy/minio`; при необходимости сверь с Dockerfile/entrypoint.)

---

## Как применять изменения

Из корня репозитория:

```bash
# остановить стек (без -v — volumes не трогать)
docker compose -f backend/deploy/docker-compose.yml down

# поднять снова
docker compose -f backend/deploy/docker-compose.yml up -d --build
```

Или: `make down` и затем `make up` (если `make up` запускаете в фоне/`-d`).

### Опасные команды

| Команда | Эффект |
|---------|--------|
| `docker compose down` | Контейнеры стоп; **с volume данные остаются** |
| `docker compose down -v` | Удаляет volumes → **полная потеря данных** |
| `docker volume rm …` | То же для конкретного volume |

Для «чистого» локального сброса осознанно используйте `down -v`.

---

## Проверка

После регистрации пользователя и `down` + `up`:

```bash
docker exec deploy-postgres-1 psql -U rokko -d auth -c "SELECT id, email, username FROM accounts;"
```

Строки должны остаться.

Проверить volume:

```bash
docker volume ls | grep postgres
```

---

## Краткий чеклист

1. Добавить `postgres_data:/var/lib/postgresql/data` к `postgres`.
2. Объявить `volumes: postgres_data:` в compose.
3. (Опционально) volumes для Redis и MinIO.
4. Не вызывать `down -v`, если данные нужны.
5. Убедиться, что после рестарта аккаунты на месте.
