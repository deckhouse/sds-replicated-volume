# Release notes

Одна пара файлов на версию модуля (git-тег):

```text
.release-notes/v1.2.3.yaml         # английский
.release-notes/v1.2.3.ru.yaml      # русский
.release-notes/next-release.yaml   # заготовка (EN) — переименовать перед тегом
.release-notes/next-release.ru.yaml
```

Имя файла обязано совпадать с тегом (`CI_COMMIT_TAG` / `MODULES_MODULE_TAG`).
Версия и имя модуля берутся из тега и `module.yaml`, в YAML их дублировать не нужно.

Это **release notes**, а не инженерный changelog: описывается то, что видит пользователь
модуля. Внутренние правки CI, рефакторинги и переезды тестов сюда не попадают.

## Схема

```yaml
date: "2026-08-25"          # необязательно, но желательно
channels:                   # необязательно
  - Stable
skip_docs: false            # необязательно; true = не показывать на странице релизов

summary: "..."              # обязательно
highlights:                 # обязательно, минимум один пункт
  - "..."

# Необязательные разделы — пустые удалять, а не оставлять `[]`:
new_features: []
improvements: []
fixes: []
security: []
breaking: []
upgrade_notes: []
known_issues: []
docs: []
dependencies:
  - name: component
    from: "1.0"
    to: "1.1"
    changelog_url: "https://..."
    notes: "..."
```

`summary` и `highlights` обязательны в **обоих** языках. Пустые списки в интерфейсе DKP
всё равно отбрасываются, поэтому раздел без пунктов не пишется вовсе.

## Куда уходит содержимое

| Куда | Артефакт |
|---|---|
| Документация модуля | `docs/RELEASE_NOTES.md` и `docs/RELEASE_NOTES.ru.md`, собираются образом `docs-generator` общим рендерером `sds-common-lib/cmd/release-notes-docs` (версия закреплена в `.werf/bundle.yaml`) |
| Релизный образ → `ModuleRelease.spec.changelog` → Console | `changelog.yaml`, куда `.werf/release.yaml` складывает обе локали под ключами `en` и `ru` |

## Проверка перед тегом

Схему проверяет джоба `Validate release notes` (стадия `pre`) из шаблона
`Release_Notes.gitlab-ci.yml` в `modules-gitlab-ci`: на теге — пару этого тега, на merge
request — все пары в каталоге. Проверяются обязательные `summary` и `highlights`, только
известные ключи, разделы как списки непустых строк, `dependencies[].name`, формат `date`,
кириллица по локалям и совпадение `skip_docs` между локалями. Валидатор встроен в шаблон,
внешних зависимостей у релиза нет.

Локально то же самое даёт сборка страницы релизов: рендерер читает файлы строго и падает на
неизвестном ключе, пустом `summary`/`highlights`, непарной локали и расхождении `skip_docs`.
Рендерер общий для всех модулей и живёт в `sds-common-lib`, версия закреплена в
`.werf/bundle.yaml`:

```bash
go run github.com/deckhouse/sds-common-lib/cmd/release-notes-docs@v0.16.0 --root .
```

Страницы `docs/RELEASE_NOTES{,.ru}.md` в git не хранятся — их собирает образ
`docs-generator` во время сборки модуля.

## История релизов

Релизы до перехода на этот формат остаются в `CHANGELOG/vX.Y.Z.yml` и `.ru.yml` с плоским
списком `Changes` / `Изменения`. Их не конвертируем: генератор читает оба каталога и рендерит
старые версии как раньше. Новые версии в `CHANGELOG/` не добавляются.
