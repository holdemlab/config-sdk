# Contributing Guide

Дякуємо за інтерес до проєкту **config-sdk**!
Цей документ описує процес розробки, стандарти коду та вимоги до Pull Request.

Загальний опис і API — [README.md](README.md); розкладка коду й gotchas —
`CLAUDE.md` (локальний файл, свідомо не в git). Цей файл — тільки про процес
контрибуції.

---

## 1. Вимоги до середовища розробки

### Go

- **Версія**: Go ≥ 1.26 (`go.mod` пінить `go 1.26.0`; CI тестує 1.26 і 1.27)
- Переконайтеся що встановлено: `go version`

### golangci-lint v2

Лінтер запускається **локально встановленим бінарником**:

```bash
go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.15.0
make lint
```

Версію тримайте синхронною з `version:` у job `lint`
[.github/workflows/ci.yml](.github/workflows/ci.yml) (зараз `v2.15`), інакше
локальний результат може не збігтися з CI.

### Залежності

Жодних. SDK — **тільки стандартна бібліотека** Go, `go.mod` не має `require`.
`GOPRIVATE`/`.netrc` не потрібні.

### Live-інтеграційні тести (необов'язково)

`make integration` ганяє тести проти справжнього Config Service і потребує
змінних середовища (без них тест пропускається):

```bash
export CONFIG_SERVICE_HOST=https://config.example.com
export CONFIG_SERVICE_TOKEN=...
export CONFIG_SERVICE_KEY=...          # 64 hex-символи
export CONFIG_TEST_CONFIG_NAME=...     # ім'я існуючого конфігу
make integration
```

---

## 2. Процес розробки

### Branch Workflow

1. **Clone** репозиторій локально.
2. **Створіть гілку** від `master`:
   ```bash
   git checkout -b <prefix>/<short-description>
   ```
3. Внесіть зміни, напишіть/оновіть тести, прогоніть лінтер.
4. Створіть **Pull Request** у `master` (CI також запускається для PR у `develop`).

### Naming Conventions для гілок

| Префікс     | Призначення                         |
|-------------|--------------------------------------|
| `feature/`  | Нова функціональність                |
| `fix/`      | Виправлення помилки                  |
| `docs/`     | Зміни в документації                 |
| `refactor/` | Рефакторинг без зміни поведінки      |
| `test/`     | Додавання або зміна тестів           |
| `chore/`    | Службові зміни (CI, інструменти)     |

**Приклади**: `feature/watch-last-event-id`, `fix/watch-backoff-overflow`,
`docs/security-notes`, `chore/bump-golangci-lint`

---

## 3. Conventional Commits

Проєкт дотримується [Conventional Commits](https://www.conventionalcommits.org/)
не лише для читабельної історії, а й тому, що **типи комітів напряму
керують автотегуванням** (CI job `auto-tag`, розділ 7): кожен push у `master`
створює SemVer-тег і GitHub Release, а тип коміту визначає, яка частина
версії підніметься.

### Таблиця префіксів

| Префікс                    | Опис                                      |
|-----------------------------|-------------------------------------------|
| `feat:`                     | Нова функціональність                     |
| `fix:`                      | Виправлення помилки                       |
| `docs:`                     | Зміни в документації                      |
| `style:`                    | Форматування, без зміни логіки            |
| `refactor:`                 | Рефакторинг коду                          |
| `test:`                     | Додавання або зміна тестів                |
| `chore:`                    | Службові зміни (CI, інструменти)          |
| `perf:`                     | Оптимізація продуктивності                |
| footer `BREAKING CHANGE:` (+ `!`) | Зламна зміна публічного API/поведінки |

Типові скоупи: `client`, `transport`, `get`, `watch`, `sse`, `crypto`,
`snapshot`, `examples`, `ci`, `docs`.

### Приклади commit messages

```
feat(watch): add WatchSnapshot for concurrency-safe updates
fix(transport): stop following HTTP redirects
docs(readme): document WatchIdleTimeout
test(sse): cover event size limit boundary
chore(ci): bump golangci-lint to v2.15
```

### BREAKING CHANGE

SDK — публічна бібліотека, тож зламною вважається будь-яка зміна, що ламає
код або очікувану поведінку споживачів: сигнатури експортованих
ідентифікаторів, семантика помилок (які sentinel-и повертаються), поведінка
`Watch`, вимоги до `Options`, підвищення мінімальної версії Go. Окремо —
**wire-формат AES-GCM** (`nonce || ciphertext || tag`, без AAD): його
змінювати можна лише синхронно із сервером.

Позначаються **обов'язково footer-ом** `BREAKING CHANGE:` у тілі коміту
(після порожнього рядка), а для наочності — ще й `!` після типу/скоупу:

```
fix(client)!: require https for non-loopback hosts

BREAKING CHANGE: New rejects plain-http Host unless it is loopback
or Options.AllowInsecureHTTP is set
```

> ⚠️ **Major-версію піднімає тільки footer.** `auto-tag`
> (`mathieudutour/github-tag-action`, правила Angular) не розпізнає `!` у
> заголовку як зламну зміну: `fix!: …` без footer дасть **patch**,
> `feat!: …` — **minor**. Сам `!` — лише позначка для людей.

---

## 4. Як запускати

Команди — через `Makefile`:

```bash
make            # lint + vet + test
make test       # go test -count=1 -race ./...
make lint       # golangci-lint run ./...
make vet        # go vet ./...
make coverage   # покриття без examples/; падає нижче 90%
make compat     # тест сумісності AES-GCM із серверним форматом
make integration  # live E2E (див. розділ 1)
```

Один тест:

```bash
go test -count=1 -race -run TestName ./...
```

Форматування окремої `make`-цілі не має — використовуйте `gofmt` напряму:

```bash
gofmt -l .          # список файлів, що не відформатовані
gofmt -w .          # відформатувати
```

**Перед створенням PR обов'язково виконайте:**

```bash
gofmt -l . && make lint && make vet && make test && make coverage
```

---

## 5. Code Style

### Стандарти Go

- Весь код форматується `gofmt`.
- `go vet ./...` і `golangci-lint` (конфіг [.golangci.yml](.golangci.yml)) — 0 issues.
- **Нуль зовнішніх залежностей.** Не додавайте модулі в `go.mod` — якщо
  щось потрібно, це пишеться на stdlib.
- Не використовуйте можливості мови/stdlib новіші за мінімальну версію Go з `go.mod`.

### Помилки

- Публічні помилки — sentinel-и в [errors.go](errors.go). Обгортайте їх так,
  щоб працював `errors.Is`: `fmt.Errorf("%w: %v", ErrX, err)`.
- HTTP-статуси мапляться в одному місці — `mapStatusError` у
  [transport.go](transport.go); не дублюйте мапінг деінде.
- Повідомлення про помилки та логи не повинні містити токен, ключ, вміст
  конфігу чи значення `Host` (воно може містити userinfo).

### HTTP

- Усі не-SSE запити йдуть через `doRequest` (retry, таймаут, ліміт розміру
  відповіді, auth-заголовок) — не робіть запити напряму через `httpClient`.
- Клієнт користувача (`Options.HTTPClient`) ніколи не мутується — SDK працює
  з копією, на якій вимкнено редиректи.

### GoDoc-коментарі

Кожен експортований ідентифікатор має GoDoc-коментар, що починається з імені
символу (це перевіряє правило `exported` у `revive`).

### Вимоги до тестів

- Тільки stdlib: `testing` + `net/http/httptest`, без testify/моків-бібліотек.
- Тести лежать у `*_test.go` того ж пакета; table-driven, де кейсів кілька.
- Усе проходить під `-race`.
- Синхронізація — через канали й дедлайни, а не `time.Sleep`.
- Поріг покриття — **90%** (`make coverage`, CI-гейт).
- Ціль `make compat` / `compat_test.go` фіксує бінарну сумісність із
  сервером — не змінюйте її без узгодженої зміни формату на сервері.

---

## 6. Типові зміни в цьому репо

### А. Новий публічний метод / опція

- [ ] GoDoc на кожен новий експортований ідентифікатор
- [ ] Нова опція в `Options` — дефолт в `applyDefaults`, нуль-значення
      безпечне
- [ ] Тести (включно з помилковими шляхами), покриття ≥ 90%
- [ ] Оновити таблиці API / Options / Errors у [README.md](README.md)
- [ ] Якщо змінюється наявна поведінка — `BREAKING CHANGE` (розділ 3)

### Б. Оновлення GitHub Actions

- [ ] Кожен `uses:` пінується **повним SHA коміту** з коментарем версії:
      `uses: owner/action@<40-hex> # vX.Y.Z`
- [ ] SHA беріть з тега upstream-репозиторію (`git ls-remote --tags <repo>`);
      для annotated-тегів — SHA коміту (`^{}`), а не об'єкта тега
- [ ] При зміні версії golangci-lint — оновити й команду встановлення в
      розділі 1

### В. Підвищення мінімальної версії Go

Тримайте узгодженими: `go.mod`, `run.go` у `.golangci.yml`, `go-version` і
матрицю в обох workflow, README («Requirements»), розділ 1 цього файлу.
Це зламна зміна для споживачів.

---

## 7. Pull Request

### Вимоги до PR

1. **Назва** — у форматі Conventional Commits: `feat(scope): description`
   (при squash-merge вона стане заголовком коміту в `master` і визначить bump).
   Для зламної зміни footer `BREAKING CHANGE: …` має бути в **тілі
   squash-коміту** — перевірте поле опису коміту в діалозі merge на GitHub,
   назви PR для major недостатньо
2. **Опис** — що зроблено, навіщо, які файли змінено
3. **Тести** — `make test` і `make coverage` проходять
4. **Лінтер** — `make lint` — 0 issues
5. **Один PR — одна задача** — не змішуйте різні задачі в одному PR

### Review процес

1. Створіть PR у `master` з описом змін.
2. CI ([.github/workflows/ci.yml](.github/workflows/ci.yml)) запускає
   послідовні job-и: `lint` → `test` (матриця Go 1.26/1.27) → `coverage`
   (гейт 90%) → `compat`.
3. Reviewer перевіряє код на відповідність стандартам цього документа.
4. Після approve та зеленого CI — merge.

### Автотегування (`auto-tag`)

Після push у `master` (зокрема merge PR) job `auto-tag` автоматично, **без
ручних дій**:

- рахує bump за Conventional Commits від останнього тега `vX.Y.Z`:
  `feat` → minor, `fix`/`perf`/`revert` → patch, footer `BREAKING CHANGE:`
  → major (`!` у заголовку без footer major **не** дає, див. розділ 3);
- коміти інших типів (`docs`, `chore`, …) теж дають **patch**
  (`default_bump: patch`), тобто **кожен** push у `master` створює новий тег;
- пушить тег і в тому ж job-і створює GitHub Release з автоматично
  згенерованими нотатками.

Release створюється саме в `auto-tag`, бо тег, запушений через
`GITHUB_TOKEN`, не запускає інші workflow. [release.yml](.github/workflows/release.yml)
спрацьовує лише на теги, запушені вручну (тести + `go mod verify` + Release).

**Тому конвенція комітів (розділ 3) — не стиль-гайд, а частина релізного
процесу**: невірний тип коміту в `master` означає невірну версію релізу.
