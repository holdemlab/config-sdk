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
керують автотегуванням** (CI job `auto-tag`, розділ 7): тип коміту в `master`
визначає, чи буде новий SemVer-тег і GitHub Release, і яка частина версії
підніметься.

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
| `!` або footer `BREAKING CHANGE:` | Зламна зміна публічного API/поведінки |

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

Позначаються **знаком оклику** після типу/скоупу та/або **footer-ом**
`BREAKING CHANGE:` у тілі коміту (після порожнього рядка) — major дає будь-що
з двох. Рекомендовано обидва, footer пояснює, що саме зламано:

```
fix(client)!: require https for non-loopback hosts

BREAKING CHANGE: New rejects plain-http Host unless it is loopback
or Options.AllowInsecureHTTP is set
```

### Major ≥ 2 = новий шлях модуля `/vN`

Go вимагає, щоб для версій v2+ шлях модуля закінчувався суфіксом major-версії
(`github.com/holdemlab/config-sdk/v2`), інакше `go get` відкидає тег як
невалідний. Тому PR зі зламною зміною **в тому ж PR** має:

- змінити `module` у `go.mod` на `.../config-sdk/vN`;
- оновити всі імпорти (`internal/sse`, `examples/`, README).

`auto-tag` перевіряє це сам і **падає**, якщо обчислений major не збігається
з суфіксом у `go.mod` — тег і реліз тоді не створюються.

> ⚠️ Не відкривайте PR з гілки, коміти якої вже потрапили в `master`
> (наприклад, повторний PR зі старої гілки після squash-merge). Squash-коміт
> скопіює їхні повідомлення разом зі старим `BREAKING CHANGE:` — і `auto-tag`
> вдруге підніме major. Нова робота — нова гілка від свіжого `master`.

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

Після push у `master` (зокрема merge PR) job `auto-tag`
([scripts/auto-tag.sh](scripts/auto-tag.sh)) автоматично, **без ручних дій**:

- рахує bump за заголовками й тілами комітів від останнього тега `vX.Y.Z`:
  `type!:` або footer `BREAKING CHANGE:` → major, `feat` → minor,
  `fix`/`perf`/`revert` → patch;
- якщо з останнього тега були лише інші типи (`docs`, `style`, `refactor`,
  `test`, `chore`, `ci`) або коміти без Conventional-префікса —
  **нічого не тегує**;
- перевіряє, що major збігається з суфіксом `/vN` у `go.mod` (розділ 3),
  інакше job падає;
- створює тег і GitHub Release (з автоматично згенерованими нотатками) одним
  викликом `gh release create`.

Перевірити, який тег вийде, можна локально (нічого не створює):

```bash
git fetch --tags && DRY_RUN=1 sh scripts/auto-tag.sh
```

Release створюється саме в `auto-tag`, бо тег, створений через
`GITHUB_TOKEN`, не запускає інші workflow. [release.yml](.github/workflows/release.yml)
спрацьовує лише на теги, запушені вручну (тести + `go mod verify` + Release).

**Тому конвенція комітів (розділ 3) — не стиль-гайд, а частина релізного
процесу**: невірний тип коміту в `master` означає невірну версію релізу,
а реальний фікс без префікса `fix:` (наприклад, squash-merge з назвою PR
на кшталт «Security review») не створить релізу взагалі.
