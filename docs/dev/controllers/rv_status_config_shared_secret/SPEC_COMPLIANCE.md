# Соответствие спецификации для `rv-status-config-shared-secret-controller`

> **Примечание:** Этот контроллер соответствует стандартам проекта, описанным в [`CONTROLLER_STYLE_GUIDE.md`](../CONTROLLER_STYLE_GUIDE.md). Структура `Reconciler` упрощена: только `Cl`, `Log`, `LogAlt` (без неиспользуемых полей `rdr`, `sch`).

## Спецификация (из `docs/dev/spec_v1alpha3.md`)

### Цель
Проставить первоначальное значения для `rv.status.config.sharedSecret` и `rv.status.config.sharedSecretAlg`,
а также обработать ошибку применения алгоритма на любой из реплик из `rvr.status.conditions[type=ConfigurationAdjusted,status=False,reason=UnsupportedAlgorithm]`, и поменять его на следующий по списку алгоритмов хеширования. Последний проверенный алгоритм должен быть указан в `Message`.

В случае, если список закончился, выставить для `rv.status.conditions[type=SharedSecretAlgorithmSelected].status=False` `reason=UnableToSelectSharedSecretAlgorithm`

### Триггер
- `CREATE(RV, rv.status.config.sharedSecret == "")`
- `CREATE/UPDATE(RVR, status.conditions[type=ConfigurationAdjusted,status=False,reason=UnsupportedAlgorithm])`

### Вывод
- `rv.status.config.sharedSecret` (генерируется новый)
- `rv.status.config.sharedSecretAlg` (выбирается из захардкоженного списка по порядку)
- `rv.status.conditions[type=SharedSecretAlgorithmSelected].status=False`
- `rv.status.conditions[type=SharedSecretAlgorithmSelected].reason=UnableToSelectSharedSecretAlgorithm`
- `rv.status.conditions[type=SharedSecretAlgorithmSelected].message=[Which node? Which alg failed?]`

### Алгоритмы хеширования shared secret
- `sha256`
- `sha1`

---

## Код, соответствующий спецификации

### ✅ `controller.go` - Триггеры

**Соответствует спецификации:**
- Использует `.For(&v1alpha3.ReplicatedVolume{})` для указания основного ресурса (строка 31)
- `CreateFunc` в `predicate.Funcs` (строки 33-40): Обрабатывает `CREATE(RV, rv.status.config.sharedSecret == "")`
  - Проверяет, что `sharedSecret` не установлен
  - Возвращает `true` для обработки события
- `Watches` на `ReplicatedVolumeReplica` (строки 58-74): Обрабатывает `CREATE/UPDATE(RVR, UnsupportedAlgorithm)`
  - Использует `EnqueueRequestsFromMapFunc` для маппинга RVR → RV
  - Фильтрует только RVR с `ConfigurationAdjusted=False, reason=UnsupportedAlgorithm`
  - Соответствует стандартам проекта (использование handler для маппинга RVR на RV)

**Добавлено сверх спецификации (стандартная практика controller-runtime):**
- `GenericFunc` в `predicate.Funcs` (строки 49-56): Обрабатывает синхронизацию при старте контроллера
  - Это стандартная практика для reconciliation на старте
  - Не указано в спецификации, но необходимо для корректной работы
- Использование `builder.ControllerManagedBy` и `.For()` - соответствует стандартам проекта

**Не требуется спецификацией:**
- `UpdateFunc` (строки 41-44): No-op, так как `sharedSecret` неизменяем после установки (кроме случаев ошибки алгоритма)
- `DeleteFunc` (строки 45-48): No-op, так как удаление не требует генерации shared secret

### ✅ `reconciler.go` - Основная логика

**Соответствует спецификации:**

1. **Использование стандартного `reconcile.Reconciler`** (строки 25, 35-38)
   - Использует стандартный `reconcile.Request` вместо кастомных типов
   - Соответствует стандартам проекта
   - Структура `Reconciler` упрощена: только `Cl`, `Log`, `LogAlt` (без неиспользуемых полей)

2. **Structured logging** (строка 39)
   - Использует `logr.Logger` с `.WithName()` и `.WithValues()`
   - Соответствует стандартам проекта

3. **Генерация `sharedSecret`** (строки 68-69, 96)
   - Генерирует новый UUID для `sharedSecret` при создании RV
   - Использует `uuid.New().String()` для генерации

4. **Выбор алгоритма из списка по порядку** (строки 27-33, 70, 162-183)
   - Список алгоритмов: `[sha256, sha1]` (строки 27-33)
   - При создании выбирает первый алгоритм `sha256` (строка 70)
   - При ошибке переключается на следующий алгоритм (строки 162-183)

5. **Обработка ошибки `UnsupportedAlgorithm`** (строки 125-208)
   - Проверяет все RVR для данного RV на наличие ошибки (строки 131-151)
   - Находит текущий алгоритм и переключается на следующий (строки 160-183)
   - Генерирует новый `sharedSecret` при переключении алгоритма (строка 202)

6. **Установка условия при исчерпании алгоритмов** (строки 210-253)
   - Устанавливает `SharedSecretAlgorithmSelected=False` с `reason=UnableToSelectSharedSecretAlgorithm`
   - В `message` указывает последний алгоритм и узлы с ошибками (строка 246)

7. **Установка `rv.status.config.sharedSecret` и `rv.status.config.sharedSecretAlg`** (строки 96-97, 202-203)
   - Использует `PatchStatusWithConflictRetry` для безопасных обновлений
   - Устанавливает оба поля одновременно
   - Инициализация status происходит внутри `patchFn` (не в начале `Reconcile`)

8. **Идемпотентность** (строки 52-54, 81-85)
   - Проверяет, не установлен ли уже `sharedSecret` перед установкой (строка 52)
   - Если установлен, выходит без изменений
   - Внутри `patchFn` также проверяет, не установлен ли уже `sharedSecret` (строки 81-85)

---

## Код, добавленный сверх спецификации

### ⚠️ `PatchStatusWithConflictRetry` - Обработка параллелизма

**Файл:** `reconciler.go`, строки 71, 177, 231

**Статус:** Техническая деталь реализации, не указана в спецификации

**Что делает:**
- Использует optimistic locking для безопасной параллельной обработки
- При конфликте (409) перезагружает ресурс и повторяет попытку
- Проверяет внутри `patchFn`, не установлен ли уже `sharedSecret` (idempotent check)
- Инициализация status происходит внутри `patchFn` (не в начале `Reconcile`)

**Почему добавлено:**
- Спецификация не описывает технические детали обработки параллелизма
- Необходимо для корректной работы при параллельной обработке запросов несколькими воркерами
- Без этого возможны race conditions при генерации shared secret

**Соответствие спецификации:**
- ✅ Спецификация требует генерацию `sharedSecret` - это обеспечивается
- ✅ Спецификация не запрещает использование retry механизмов
- Это техническая деталь реализации, необходимая для корректной работы

### ⚠️ `GenericFunc` - Reconciliation на старте

**Файл:** `controller.go`, строки 49-56

**Статус:** Стандартная практика controller-runtime, не указана в спецификации

**Что делает:**
- Обрабатывает события синхронизации при старте контроллера
- Проверяет все существующие RV и добавляет в очередь те, у которых `sharedSecret` не установлен

**Почему добавлено:**
- Стандартная практика для Kubernetes контроллеров
- Обеспечивает обработку RV, созданных до старта контроллера
- Не указано в спецификации, но необходимо для корректной работы

### ⚠️ Установка условия `SharedSecretAlgorithmSelected=True` при успешном выборе

**Файл:** `reconciler.go`, строки 105-114, 210-219

**Статус:** Добавлено для улучшения наблюдаемости, не указано в спецификации

**Что делает:**
- Устанавливает `SharedSecretAlgorithmSelected=True` с `reason=AlgorithmSelected` при успешной генерации или переключении алгоритма
- В `message` указывает выбранный алгоритм

**Почему добавлено:**
- Спецификация требует только установку `False` при ошибке
- Добавлено для улучшения наблюдаемости - администраторы видят успешный выбор алгоритма
- Это стандартная практика для условий в Kubernetes

**Как откатить:**
- Удалить установку условия `True` (строки 105-114, 210-219)
- Оставить только установку `False` при ошибке (строки 245-255)

---

## Итоговая таблица соответствия

| Требование спецификации | Статус | Расположение в коде |
|------------------------|--------|-------------------|
| Генерация `sharedSecret` при создании RV | ✅ Соответствует | `reconciler.go:68-69, 96` |
| Выбор алгоритма из списка по порядку | ✅ Соответствует | `reconciler.go:27-33, 70, 162-183` |
| Обработка ошибки `UnsupportedAlgorithm` | ✅ Соответствует | `reconciler.go:125-208` |
| Переключение на следующий алгоритм | ✅ Соответствует | `reconciler.go:175-183` |
| Установка условия `False` при исчерпании алгоритмов | ✅ Соответствует | `reconciler.go:210-253` |
| Триггер `CREATE(RV, sharedSecret == "")` | ✅ Соответствует | `controller.go:33-40` |
| Триггер `CREATE/UPDATE(RVR, UnsupportedAlgorithm)` | ✅ Соответствует | `controller.go:58-74` |
| Вывод `rv.status.config.sharedSecret` | ✅ Соответствует | `reconciler.go:96, 202` |
| Вывод `rv.status.config.sharedSecretAlg` | ✅ Соответствует | `reconciler.go:97, 203` |
| Вывод условия с `reason=UnableToSelectSharedSecretAlgorithm` | ✅ Соответствует | `reconciler.go:248-250` |
| Вывод `message` с информацией об узлах и алгоритме | ✅ Соответствует | `reconciler.go:246` |
| Обработка параллелизма через retry | ⚠️ Техническая деталь | `reconciler.go:71, 177, 231` |
| Reconciliation на старте | ⚠️ Стандартная практика | `controller.go:49-56` |
| Установка условия `True` при успехе | ⚠️ Сверх спецификации | `reconciler.go:105-114, 210-219` |
| Стандартный reconcile.Reconciler | ✅ Соответствует стандартам | `reconciler.go:25, 35-38` |
| Использование .For() | ✅ Соответствует стандартам | `controller.go:29` |
| Structured logging | ✅ Соответствует стандартам | `reconciler.go:39` |
| Упрощенная структура Reconciler (только Cl, Log, LogAlt) | ✅ Соответствует стандартам | `reconciler.go:19-23` |

---

## Итоги

1. **Код, соответствующий спецификации:** Оставлен как есть, полностью соответствует требованиям.

2. **Код сверх спецификации:**
   - `PatchStatusWithConflictRetry`: Необходимо оставить для корректной работы при параллелизме
   - `GenericFunc`: Необходимо оставить для стандартной работы контроллера
   - Установка условия `True` при успехе: Можно оставить для улучшения наблюдаемости, или удалить для строгого соответствия спецификации

