# Ansible: аудит и план оптимизации

Дата проверки: 13 июля 2026 года. Область проверки:
`deploy/ansible/vps-edge.yml`, `reverse-ssh-links.yml`,
`edge-and-links.yml`, templates, inventory examples и Ansible README.

## Итог

Playbook функционально охватывает весь edge lifecycle, но стал монолитом:
`vps-edge.yml` содержит около 2 000 строк и 154 tasks, включая 38 `command`,
28 `set_fact`, установку ОС-пакетов, DNS repair, сборку Go, certbot двумя
способами, nginx, systemd, health registration и итоговые проверки. Это
увеличивает время rollout, усложняет частичный запуск и делает отказ в конце
playbook труднее откатываемым.

Главная оптимизация — не сокращение отдельных tasks, а разделение ответственности
и переход от сборки на каждом VPS к immutable artifacts.

## Целевая структура

```text
deploy/ansible/
  playbooks/
    edge.yml
    links.yml
  roles/
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
| P0 | `reverse_logger_go_sumdb: "off"` отключает независимую проверку модулей | Сделать `sum.golang.org`/trusted internal sumdb стандартом; `off` разрешать только явным аварийным override | Production lint запрещает `off` |
| P1 | Два Go-бинарника собираются на каждом VPS | Собирать один раз в CI/control plane, публиковать checksum, signature и SBOM, устанавливать атомарно | На target нет Go toolchain/source checkout |
| P1 | Нет `serial`, canary и `max_fail_percentage` | Использовать rolling rollout, например `serial: 1` для canary, затем batch; throttle ACME/API | Ошибка canary останавливает парк |
| P1 | Финальная регистрация expected health node не имеет retry | Применить bounded retry/backoff и отличать transient HTTP от auth/config error | Краткий outage main не ломает deploy |
| P1 | Общий HTTP control-plane считается нормой | Assert: HTTP допустим только для явно обозначенной private encrypted network; целевое значение HTTPS/mTLS | Публичный cleartext deployment отклоняется |
| P1 | `nginx_edge_fix_resolv_conf` по умолчанию меняет системный symlink | Default `false`; менять resolver только после проверки systemd-resolved и с backup/rollback | Playbook не ломает NetworkManager/cloud-init DNS |
| P1 | Поздняя ошибка может не выполнить ожидающий handler | Добавить осознанные `meta: flush_handlers` после валидированной конфигурации, `force_handlers` только там, где безопасно | После отказа сервис и файлы одной версии |
| P1 | Partial tags зависят от untagged prerequisites | Сделать tags самодостаточными или маркировать `edge_health`/`snap` как incremental-only | Fresh-host tag test либо проходит, либо блокируется понятным assert |
| P1 | Certbot snap/pip fallback занимает большую долю monolith | Вынести в `edge_tls`, использовать модули `snap`/`pip`, закрепить версии и кэшировать установку | TLS role тестируется отдельно |
| P2 | Большой начальный `set_fact` повышает precedence | Перенести defaults в роли, derived facts оставить локально возле потребителя | Нет giant defaults task |
| P2 | Nginx config копируется/валидируется несколькими фазами | Один role, templates/includes, validate перед atomic replace, handlers | Одна точка сборки effective config |
| P2 | Minimum Go check не гарантирует security patch level | Проверять утвержденный toolchain version или полностью убрать Go с target | Старый уязвимый patch блокируется |
| P2 | `GOTOOLCHAIN=local` полезен, но маскирует рассинхрон toolchain | Artifact build; до миграции assert точной совместимой версии | Ошибка появляется до download/build |
| P2 | Pip/snap certbot packages обновляются без release pin | Закрепить поддерживаемые версии и план обновления | Повторяемая установка зафиксированной версии |
| P2 | Проверка `vps_internal_ip` — только regex IPv4 | Использовать `ansible.utils.ipaddr`, поддержать IPv6 или явно запретить | Невалидные октеты отклоняются |
| P2 | Source checkout и build выполняются root на VPS | Убрать checkout; временно использовать build user и atomic `install` | Source/build не дают лишнюю root surface |
| P2 | Нет lock от двух одновременных запусков и генерации links | CI environment lock/`flock`, уникальный artifact dir, atomic publish | Параллельный deploy не смешивает состояние |
| P2 | Явный daemon-reload дублирует handler | Оставить один idempotent handler и flush в нужной точке | Нет лишнего reload/change report |
| P3 | Network probes повторяются в нескольких ветках | Сделать общий probe result fact и переиспользовать | Один диагностируемый preflight |
| P3 | Download trace включен постоянно | Включать verbose trace при retry/failure или debug flag | Обычный лог краток, debug остается доступным |

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

### Шаг 3. Перейти на artifacts

CI cross-compiles Linux binaries, создает checksum/SBOM/signature. Ansible
скачивает или получает artifact с control node, проверяет checksum, кладет во
временный путь и выполняет atomic rename. Старый бинарник сохраняется для
rollback.

### Шаг 4. Ввести rolling orchestration

Отдельный canary play, `serial`, health gate после каждого batch, throttle для
ACME/Timeweb и автоматическая остановка при превышении error budget. Генерация
ссылок запускается только после успешного edge rollout.

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

