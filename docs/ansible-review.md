# Ansible: аудит и план оптимизации

Дата проверки: 13 июля 2026 года. Область проверки:
`deploy/ansible/vps-edge.yml`, `reverse-ssh-links.yml`,
`edge-and-links.yml`, templates, inventory examples и Ansible README.

## Итог

Playbook функционально охватывает весь edge lifecycle, но стал монолитом:
`vps-edge.yml` содержит более 2 000 строк и более 150 tasks, включая множество
`command`/`set_fact`, установку ОС-пакетов, DNS repair, сборку Go, certbot двумя
способами, nginx, systemd, health registration и итоговые проверки. Это
увеличивает время rollout, усложняет частичный запуск и делает отказ в конце
playbook труднее откатываемым.

Главная оптимизация — не сокращение отдельных tasks, а разделение ответственности
и переход от сборки на каждом VPS к immutable artifacts.

## Выполнено в быстрой итерации

- введён rolling rollout: один canary, затем 25% batches, нулевой допустимый
  процент ошибок;
- ACME-вызовы throttled до одного host, регистрация expected health node получила
  bounded retry/backoff;
- повторный запуск сверяет commit-маркеры установленных бинарников и пропускает
  Go proxy probes, module download и build, если checkout не менялся;
- `GOSUMDB=sum.golang.org`, краткий Go download output и отсутствие системной
  правки `/etc/resolv.conf` стали безопасными defaults;
- handlers выполняются после `nginx -t` и установки unit/env, до поздней health
  registration; отдельный дублирующий `daemon_reload` удалён;
- сбор фактов ограничен minimum + network subsets.
- добавлен основной artifact-flow: два Go-бинарника собираются один раз на
  controller для каждой архитектуры, кэш проверяется по SHA-256, а VPS больше
  не требует Git/Go/source checkout;
- релиз устанавливается в versioned directory через `pending.json`, после
  handlers и health gate повышается до `active.json`, предыдущий релиз остаётся
  доступным для rolling rollback;
- `deploy_edge.py` ставит controller lock, запускает rollout и не допускает
  публикацию links при ненулевом коде возврата любой batch;
- rollout завершён до трёх волн `1 → 25% → остаток`, добавлены local service,
  listener и public HTTPS gates.

Прямой запуск `vps-edge.yml` временно сохраняет source-build compatibility.
Рекомендуемый production entry point — `deploy_edge.py`, который использует
controller artifacts. Следующие извлекаемые границы — transactional nginx/TLS
и спецификация/ротация links.

## Целевая структура

```text
deploy/ansible/
  playbooks/
    edge-rollout.yml
    links-publish.yml
    edge-rollback.yml
  roles/
    reverse_logger_artifact_build/
    reverse_logger_artifact/
    edge_verify/
    edge_preflight/
    edge_network/
    reverse_logger_artifacts/
    edge_tls/
    edge_nginx/
    edge_forwarder/
    edge_health/
    edge_verify/
  group_vars/
  host_vars/
  molecule/
```

Defaults находятся в `roles/*/defaults/main.yml`, обязательные production
значения проверяются в `assert`, секреты остаются в vault/external secret
store, а handlers принадлежат соответствующим ролям.

## Находки и рекомендации

| Приоритет | Наблюдение | Рекомендация | Критерий готовности |
| --- | --- | --- | --- |
| P0 | `reverse_logger_repo_version` по умолчанию равен `main` | Production inventory обязан задавать immutable tag/commit; лучше устанавливать подписанный artifact по checksum | Два одинаковых запуска устанавливают одинаковые байты |
| P0 | Исправлено: checksum DB по умолчанию — `sum.golang.org`; `off` остаётся аварийным override | Добавить production lint, запрещающий неявное отключение sumdb | Production lint запрещает `off` |
| P1 | Исправлено для основного workflow: controller собирает один artifact на архитектуру, target проверяет SHA-256 и переключает versioned release | Перенести ту же сборку в CI, добавить SBOM/signature, затем удалить target-source compatibility | На target нет Go toolchain/source checkout |
| P1 | Исправлено: canary → 25% → остаток, `max_fail_percentage: 0`, ACME throttle и post-host/batch health gate | Добавить наблюдаемое soak-окно между canary и массовой волной при росте парка | Ошибка canary останавливает парк |
| P1 | Исправлено частично: expected health registration имеет bounded retry/backoff | Отличать transient HTTP от auth/config error без лишних повторов | Краткий outage main не ломает deploy |
| P1 | Общий HTTP control-plane считается нормой | Assert: HTTP допустим только для явно обозначенной private encrypted network; целевое значение HTTPS/mTLS | Публичный cleartext deployment отклоняется |
| P1 | Исправлено: `nginx_edge_fix_resolv_conf` по умолчанию `false` | При opt-in добавить проверку systemd-resolved и backup/rollback | Playbook не ломает NetworkManager/cloud-init DNS |
| P1 | Исправлено: handlers flush после `nginx -t` и до поздней регистрации | Оставить `force_handlers` выключенным, пока не доказана безопасность каждого handler | После отказа сервис и файлы одной версии |
| P1 | Partial tags зависят от untagged prerequisites | Сделать tags самодостаточными или маркировать `edge_health`/`snap` как incremental-only | Fresh-host tag test либо проходит, либо блокируется понятным assert |
| P1 | Certbot snap/pip fallback занимает большую долю monolith | Вынести в `edge_tls`, использовать модули `snap`/`pip`, закрепить версии и кэшировать установку | TLS role тестируется отдельно |
| P2 | Большой начальный `set_fact` повышает precedence | Перенести defaults в роли, derived facts оставить локально возле потребителя | Нет giant defaults task |
| P2 | Nginx config копируется/валидируется несколькими фазами | Один role, templates/includes, validate перед atomic replace, handlers | Одна точка сборки effective config |
| P2 | Minimum Go check не гарантирует security patch level | Проверять утвержденный toolchain version или полностью убрать Go с target | Старый уязвимый patch блокируется |
| P2 | `GOTOOLCHAIN=local` полезен, но маскирует рассинхрон toolchain | Artifact build; до миграции assert точной совместимой версии | Ошибка появляется до download/build |
| P2 | Pip/snap certbot packages обновляются без release pin | Закрепить поддерживаемые версии и план обновления | Повторяемая установка зафиксированной версии |
| P2 | Проверка `vps_internal_ip` — только regex IPv4 | Использовать `ansible.utils.ipaddr`, поддержать IPv6 или явно запретить | Невалидные октеты отклоняются |
| P2 | Source checkout и build выполняются root на VPS | Убрать checkout; временно использовать build user и atomic `install` | Source/build не дают лишнюю root surface |
| P2 | Исправлено локально: `deploy_edge.py` держит advisory lock и публикует links только после успешного rollout | Добавить CI environment lock при переносе запуска в CI | Параллельный deploy не смешивает состояние |
| P2 | Исправлено: явный daemon-reload удалён, используется handler + flush | Сохранить единственную точку reload при декомпозиции на роли | Нет лишнего reload/change report |
| P3 | Network probes повторяются в нескольких ветках | Сделать общий probe result fact и переиспользовать | Один диагностируемый preflight |
| P3 | Исправлено: download trace выключен по умолчанию | Включать `reverse_logger_go_download_trace` только для диагностики | Обычный лог краток, debug остается доступным |

## План рефакторинга без большого взрыва

### Шаг 1. Зафиксировать текущее поведение

- добавить syntax, check-mode и второй idempotency run в CI;
- создать Molecule scenario для Ubuntu целевой версии;
- сохранить effective nginx/systemd/health assertions как acceptance tests;
- проверить full deploy, `--limit`, `--tags edge_health` и rollback.

### Шаг 2. Вынести роли без изменения поведения

Сначала `edge_health` и `edge_forwarder`, затем nginx/TLS, после этого
preflight/network. Каждый перенос делается отдельным commit и сравнивает
effective files и enabled services.

### Шаг 3. Перейти на artifacts — выполнен локальный control-plane этап

Ansible controller cross-compiles Linux binaries один раз на архитектуру,
создаёт SHA-256/manifest, копирует versioned release и переключает `current`.
`active.json`/`previous.json` обеспечивают rollback без повторной сборки.
Оставшаяся часть шага — перенести build в CI и добавить SBOM/signature.

### Шаг 4. Завершить rolling orchestration — выполнен базовый gate

`serial`, canary, error budget, throttle и service/public health gate введены.
`deploy_edge.py` создаёт жёсткий process barrier перед links. При большом парке
следует добавить настраиваемое soak-окно и проверку fleet-level метрик, а не
только host-level readiness.

### Шаг 5. Удалить compatibility defaults

После подтверждения artifact flow убрать target Go build, alternate public Go
proxies, `GOSUMDB=off` и автоматическую правку `/etc/resolv.conf`. Оставить
аварийные варианты отдельным opt-in recovery playbook.

## Что оставить

- ранние `assert` обязательных токенов, путей и inventory;
- bounded retries для действительно transient сетевых операций;
- `validate` для nginx до reload;
- отдельные systemd units для forwarder и health-agent;
- итоговые health/backend проверки и aggregate output generated links;
- Ansible Vault/external secret handling вместо секретов в репозитории.

## Проверочный набор после оптимизации

```sh
ansible-lint deploy/ansible
ansible-playbook -i deploy/ansible/inventory.ini \
  deploy/ansible/playbooks/edge.yml --syntax-check
ansible-playbook -i <test-inventory> deploy/ansible/playbooks/edge.yml --check
molecule test
```

Дополнительно CI должен выполнить два реальных прогона на disposable VM и
потребовать `changed=0` на втором, затем проверить nginx `-t`, systemd state,
health endpoints и rollback предыдущего artifact.

## Ожидаемый эффект

- сокращение времени rollout и сетевой нагрузки за счет единственной сборки;
- воспроизводимость между VPS и быстрый rollback;
- меньший blast radius благодаря canary/batches;
- независимое тестирование TLS, nginx, health и artifact delivery;
- понятная граница между обычным deploy и аварийными recovery-настройками.

Локальный Linux smoke в Windows/Docker контуре показал controller build stage
`69,6 с → 39,2 с` на кэшированном повторе (`−43,7%`, `changed=0`; Go download и
обе сборки skipped). Более важная масштабная метрика не зависит от тестового
контура: для 100 VPS одной архитектуры число Go builds уменьшается с 200 до 2
(`−99%`), а последовательные rollout waves — с 5 до 3 (`−40%`). Реальные
fleet p50/p95 необходимо замерить после первого production rollout.

Архитектурный контракт и альтернативы зафиксированы в
[`ADR 0001`](architecture/adr/0001-scalable-ansible-edge-rollout.md).

