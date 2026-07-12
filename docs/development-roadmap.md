# План развития reverse_ssh Monitoring Stack

Статус документа: 13 июля 2026 года. Это отдельный рабочий backlog проекта,
составленный после сквозного аудита кода, контейнеров, Ansible, Terraform,
systemd, сетевого контура и документации. Он не означает, что перечисленные
изменения уже внедрены.

## Как пользоваться планом

Приоритеты:

- **P0** — риск потери событий, компрометации или поломки production;
- **P1** — надежность и управляемость, необходимые до расширения парка VPS;
- **P2** — масштабирование, качество сопоставления и эксплуатационная зрелость;
- **P3** — удобство разработки и дальнейшее улучшение архитектуры.

Задача считается закрытой только после реализации, автоматической проверки,
обновления соответствующего runbook и проверки отката. Порядок ниже учитывает
зависимости: сначала безопасность и сохранность событий, затем масштабирование.

## Текущее состояние

Сильные стороны текущего решения:

- разделены lifecycle webhook, nginx ingress, live snapshot и edge-health;
- ingress имеет локальный spool, а Telegram — outbox и дедупликацию;
- dashboard и SQLite дают единое место для диагностики;
- основные секреты вынесены в окружение, а публичные порты ограничиваются UFW
  и `DOCKER-USER`;
- документация уже покрывает архитектуру, ручной и Ansible-деплой, firewall,
  webhook, WSS/HTTPS и ежедневные операции.

Основные технические долги: доставка некоторых событий остается
best-effort, нет автоматической retention policy, control-plane между VPS и
main по умолчанию использует HTTP, Ansible собирает Go-бинарники на каждом VPS,
а CI и воспроизводимая цепочка поставки пока отсутствуют.

## Этап 0. Немедленное снижение риска (P0, 1–3 дня)

### 0.1. Обновить Go и уязвимые зависимости

Проблема: аудит `govulncheck` обнаружил достижимые уязвимости в используемых
версиях Go standard library и `golang.org/x/crypto`; builder image также
плавает по тегу.

Действия:

1. Обновить Go до актуального исправленного patch-релиза и
   `golang.org/x/crypto` как минимум до версии с исправлениями.
2. Зафиксировать Docker builder по digest или точному patch-тегу.
3. Добавить `govulncheck` и проверку зависимостей в CI.

Готово, когда `govulncheck ./...` не показывает достижимых известных
уязвимостей, образы воспроизводимы, а версии отражены в changelog.

### 0.2. Исправить lifecycle Compose в systemd

Проблема: репозиторный `rssh-monitor.service` вызывает обычные
`docker compose up/down` и не включает `docker-compose.edge-forward.yml`.
Оператор может получить стек без публикации logger `8080` либо остановить не
тот набор сервисов.

Действия: сделать единый Compose wrapper или EnvironmentFile со списком
Compose-файлов; одинаково использовать его для deploy, systemd, rollback и
диагностики. До изменения unit применять override из manual deployment.

Готово, когда `systemctl start/stop/restart rssh-monitor` проверен на чистом
хосте, `docker compose config` содержит edge override, а тест ловит расхождение
набора файлов.

### 0.3. Не подтверждать edge-health alert до доставки

Проблема: переход состояния может быть отмечен как уведомленный до успешного
ответа Telegram. При ошибке доставки recovery/degraded alert теряется.

Действия: фиксировать `notified` только после подтвержденной доставки либо
создавать транзакционную outbox-запись в той же транзакции, что и переход.

Готово, когда тест имитирует отказ Telegram, последующий retry отправляет ровно
одно сообщение, а рестарт процесса не теряет событие.

### 0.4. Закрыть базовые IaC-риски

Действия: добавить `.terraform/`, `*.tfstate*` и plan-файлы в `.gitignore`;
зафиксировать provider constraints; описать защищенный backend с locking;
исключить root password из обычных outputs и регламентировать ротацию.

Готово, когда секреты не появляются в git/CI artifacts, state зашифрован и
блокируется, а удаление production-ресурса требует осознанного действия.

## Этап 1. Гарантированная доставка и хранение (P1, 1–2 недели)

### 1.1. Полноценный Telegram worker

Текущий outbox не имеет независимого фонового replay. Добавить worker с
exponential backoff, jitter, лимитом попыток, dead-letter состоянием и
метриками. Повтор webhook не должен быть единственным способом восстановить
доставку.

Готово, когда outage Telegram на несколько часов не теряет сообщения, порядок
событий определен, а dashboard показывает очередь и последнюю ошибку.

### 1.2. Надежный forwarder ошибок reverse_ssh

Текущий journal/docker follower начинает с текущего момента и не хранит cursor
или spool; неуспешный POST теряется. Добавить journal cursor, disk spool,
atomic rename, ограничение размера и dead-letter. Для Docker определить
устойчивый checkpoint или явно оставить режим диагностическим.

### 1.3. Retention, backup и capacity policy

При 24 VPS и периоде health 30 секунд создается около 25 миллионов health rows
в год. Ввести настраиваемое хранение сырых health reports, агрегацию состояния,
очистку JSONL/SQLite, `VACUUM`/checkpoint и мониторинг свободного места.

Готово, когда заданы RPO/RTO, проверено восстановление backup, а годовой рост
рассчитан и ограничен политикой.

### 1.4. Атомарность журналов и graceful shutdown

Устранить расхождение, при котором JSONL может быть записан до неуспешного DB
commit. Добавить корректное завершение HTTP server/workers, fsync policy для
критичных spool-файлов и интеграционные crash-тесты.

### 1.5. Контроль полноты telemetry

Добавить consistency alert: есть live snapshot или ingress, но в допустимое
окно не пришел lifecycle webhook. Отдельно контролировать давность snapshot,
очереди forwarder и регистрацию webhook.

## Этап 2. Усиление trust boundaries (P1, 2–4 недели)

### 2.1. Шифрование VPS → main

Ingress forwarding и edge-health по умолчанию формируют HTTP URL. Это допустимо
только внутри реально шифрованной и изолированной сети. Целевое состояние:
HTTPS с проверкой CA и желательно mTLS, либо подтвержденный WireGuard/VPN
маршрут; cleartext через публичную сеть запрещен проверкой конфигурации.

### 2.2. Раздельная идентичность VPS

Заменить общие токены на per-VPS credentials или HMAC с timestamp/nonce.
Добавить replay protection, ротацию без простоя и аудит вызывающего узла.

### 2.3. Ограничение ключа reconciler

Сейчас ключ предназначен для console `ls`, но reverse_ssh не обеспечивает в
этом проекте доказанное command-level ограничение. Считать его административным
credential до реализации отдельного read-only API/RBAC. Цель — специальный
snapshot endpoint или ключ с технически проверяемым forced-command policy.

### 2.4. Защита download и upstream TLS

Определить модель доступа к `/dl`, убрать публичную раздачу без авторизации,
если это не бизнес-требование. Включить проверку TLS upstream вместо
`proxy_ssl_verify off`, закрепить CA/SNI и добавить negative tests.

### 2.5. Hardening контейнеров и firewall lifecycle

Добавить non-root там, где возможно, read-only filesystem, drop capabilities,
`no-new-privileges`, resource limits, healthchecks и log rotation. Обеспечить
автоматическое восстановление `DOCKER-USER` guard после перезапуска Docker и
проверку effective rules, а не только UFW.

## Этап 3. Корреляция и модель сессий (P2, 2–3 недели)

1. Ввести идентификатор экземпляра соединения, чтобы повторно использованный
   `reverse_ssh_id` не наследовал ingress старой сессии.
2. Ограничить наследование connect metadata по времени и состоянию; при
   сомнении показывать `unknown`, а не уверенный старый match.
3. Пересмотреть лимит 16 ingress-кандидатов и индексы под реальный объем.
4. Сделать fallback correlation явной политикой с confidence/reason в UI.
5. Валидировать timestamp webhook, clock skew и replay window.
6. Очищать stale client/network/ingress metadata в snapshot-представлении.
7. Разделить штатный EOF/keepalive disconnect и действительно ошибочные
   попытки, чтобы не создавать ложные generic alerts.

Готово, когда набор table-driven и end-to-end тестов покрывает повтор ID,
перестановку событий, задержанный ingress, clock skew, дубликаты и рестарты.

## Этап 4. Рефакторинг Ansible (P1/P2, 2–4 недели)

Полный разбор и конкретные рекомендации находятся в
[Ansible: аудит и план оптимизации](ansible-review.md). Целевые показатели:

- основной playbook короче 150 строк и собирается из ролей;
- production deploy закреплен на commit/tag или artifact digest, не `main`;
- на VPS не компилируются Go-бинарники;
- `GOSUMDB=off` не является production default;
- rollout использует canary/`serial`, а ACME и внешние API throttled;
- каждый поддерживаемый tag либо самодостаточен, либо явно помечен как
  incremental-only;
- повторный прогон не меняет систему, Molecule/check-mode проходят в CI;
- handler checkpoints не оставляют активный сервис со старой конфигурацией.

## Этап 5. CI/CD и эксплуатационная зрелость (P2, 2–4 недели)

### CI quality gates

- `gofmt`, `go test ./...`, `go vet`, `govulncheck` и Linux race tests;
- Python tests, `shellcheck`, Compose config matrix;
- `ansible-lint`, YAML lint, syntax/check/idempotency и Molecule;
- `terraform fmt/validate/tflint` и secret scanning;
- Markdown link checker и проверка примеров Compose/systemd;
- сборка SBOM, checksum/signature artifacts и container scan.

Windows-тесты должны нормализовать CRLF: текущая проверка Compose regex
зависит от LF и дает ложный отказ на Windows, хотя функциональные тесты
проходят.

### Release process

Ввести versioned releases, immutable images/artifacts, changelog на релиз,
canary deploy, автоматическую smoke-проверку и задокументированный rollback.
Добавить `SECURITY.md`, лицензию, правила disclosure и ownership критичных
компонентов.

## План обновления документации

| Источник истины | Документы-потребители | Автоматическая проверка |
| --- | --- | --- |
| Compose config | README, manual, operations, systemd | `docker compose config` для полного набора файлов |
| `.env.example` и config code | architecture, manual, Ansible vars | тест наличия и одинаковых имен переменных |
| HTTP routes | architecture, webhook, nginx guide | route inventory/test |
| systemd units | manual, operations | `systemd-analyze verify` и smoke test |
| UFW/`DOCKER-USER` policy | firewall, manual | effective-rule audit script |
| Ansible defaults | Ansible README | generated defaults table или lint check |
| SQLite migrations | architecture/storage | schema dump comparison |

Правила сопровождения:

1. Любое изменение порта, route, переменной, Compose-файла или unit требует
   изменения документации в том же PR.
2. Production-specific IP и секреты не коммитятся; в репозитории остаются
   только placeholders и процедура получения актуального списка.
3. Раз в квартал выполняется doc review с датой проверки в changelog.
4. Команды деплоя всегда показывают полный Compose file set; сокращенная
   команда допустима только с явно указанным условием.
5. Ограничения называются техническими только при наличии enforcement/test.

## Рекомендуемый порядок поставки

1. **Release A — безопасность:** обновления Go/x/crypto, systemd Compose fix,
   Terraform hygiene, исправление edge alert transaction.
2. **Release B — доставка:** Telegram replay worker, error spool/cursor,
   telemetry consistency alert.
3. **Release C — эксплуатация:** retention/backup, graceful shutdown,
   container/firewall hardening.
4. **Release D — supply chain:** artifact-based Ansible, роли, rolling rollout,
   CI security gates.
5. **Release E — модель сессий:** session-instance identity и обновленная
   correlation policy.
6. **Release F — zero-trust control plane:** mTLS/per-node credentials и
   read-only snapshot interface.

Каждый релиз сначала проходит на одном canary VPS, затем на малой группе и
только после наблюдаемого окна — на всем парке.

## Метрики готовности проекта

- доля потерянных принятых событий: 0 при восстановимой внешней ошибке;
- Telegram outbox age и forwarder spool age имеют SLO и alert;
- webhook/ingress/snapshot consistency измеряется автоматически;
- restore backup и rollback релиза регулярно проверяются;
- deploy повторяем, idempotent и закреплен на immutable версии;
- неизвестные уязвимости проверяются на каждом build, известные P0 не
  остаются открытыми;
- документация проходит link/config checks и имеет владельца.

