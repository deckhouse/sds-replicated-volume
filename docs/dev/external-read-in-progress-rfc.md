# RFC: транзишн ExternalReadInProgress на ReplicatedVolume

Статус: черновик
Автор: сопровождающие rv-controller
Нормативные ключевые слова (MUST / SHOULD / MAY) — по [RFC 2119] / [RFC 8174] (см. также `.cursor/rules/rfc-like-mdc.mdc`).

## 1. Аннотация

RFC предлагает новый datamesh-транзишн на *`ReplicatedVolume`* (RV) — `ExternalReadInProgress`. Он позволяет `rv-controller` сериализовать изменения топологии RV против внешних потребителей, читающих данные RV (конвейер снапшота и чтение клон-источника). Пока существует хотя бы один такой потребитель, на источнике-RV MUST быть активен этот транзишн; пока он активен, транзишны, переключающие `multiattach` (в первую очередь `EnableMultiattach`), MUST быть отклонены при попытке создания.

## 2. Мотивация

Две существующие операции требуют, чтобы источник-RV имел один primary с приостановленным вводом-выводом, иначе нельзя получить согласованный снимок на момент времени:

1. *`ReplicatedVolumeSnapshot`* (RVS) — `rvs-controller` выполняет `SuspendIO + FlushBitmap` на единственном primary, после чего снимает LVM-снапшоты с подложенных LV.
2. Клонирование RV→RV — когда у целевого RV `spec.dataSource.Kind=ReplicatedVolume`, `rv-controller` делает LVM thin-clone на primary-узле источника и доводит реплики через DRBD initial sync.

В обоих случаях, если параллельно активен второй primary и принимает запись, итоговый образ перестаёт быть согласованным снимком на момент времени: запись на втором primary проскальзывает мимо `SuspendIO` / `FlushBitmap`, а LVM thin-clone фиксирует только то, что видит primary источника.

Сегодня это решается реактивно, постфактум:

- `rvs-controller` в `reconcileMultiPrimary` проверяет `rv.IsMultiPrimary()` и, если истинно, переводит RVS в `Failed`.
- Для клонирования RV→RV аналогичной проверки нет вообще.

Это реактивно (RVS / клон обязан упасть), непоследовательно (снапшот защищён, клон — нет) и размазано по двум контроллерам. Хочется один механизм, владельцем которого является `rv-controller`, который запрещает конфликтующее изменение топологии в исходной точке (на самом RV) и одинаково покрывает всех текущих и будущих «внешних читателей».

## 3. Цели

1. Сделать невозможным создание `EnableMultiattach`, пока RV читается внешним потребителем.
2. Выразить контракт на самом RV (через `status.datameshTransitions`), чтобы он был наблюдаемым, отлаживаемым и видимым пользовательским инструментам — как любой другой datamesh-транзишн.
3. Переместить ответственность в `rv-controller`; `rvs-controller` и поток клонирования не должны знать друг о друге и дублировать одну и ту же оборонительную логику.
4. Покрыть RVS и клонирование RV→RV одним механизмом. (Восстановление из RVS — `Kind=ReplicatedVolumeSnapshot` — вне области: оно не читает RV напрямую.)

## 4. Не-цели

1. Полностью запретить RV с несколькими primary. Состояние с несколькими primary остаётся валидным устойчивым состоянием вне окон, описанных в этом RFC.
2. Заменить существующий финализатор `RVCloneSourceFinalizer`, который защищает источник-RV от удаления во время формирования клона. Финализатор охраняет жизненный цикл самого объекта RV; этот RFC охраняет топологию уже существующего RV.
3. Сама логика приостановки ввода-вывода (`SuspendIO`, `FlushBitmap`, операции LVM) — остаётся в `rvs-controller` / `rvr-controller` / агенте.
4. Менять схемы CRD *`ReplicatedVolumeSnapshot`* или *`ReplicatedVolume`*. Предложение расширяет `status.datameshTransitions` новым типом транзишна, но не вводит новых полей верхнего уровня.

## 5. Контекст

### 5.1. dmte (datamesh transition engine)

`images/controller/internal/controllers/rv_controller/dmte/` — обобщённый движок для многошаговых транзишнов на RV. Каждый транзишн имеет:

- *тип* (`v1alpha1.ReplicatedVolumeDatameshTransitionType`),
- *группу* (`v1alpha1.ReplicatedVolumeDatameshTransitionGroup`), используемую *Tracker*'ом для контроля допуска,
- *план* (последовательность шагов с `Apply`/`Confirm`/опциональным `OnComplete`),
- опциональные *Guards*, выполняемые перед созданием.

Транзишны хранятся в `rv.Status.DatameshTransitions`. Движок работает без побочных эффектов ввода-вывода: реконсилер прогоняет движок и затем сохраняет результат.

### 5.2. Существующие группы Tracker'а

`images/controller/internal/controllers/rv_controller/datamesh/concurrency_tracker.go` распознаёт группы:

- `Formation`, `Quorum`, `Multiattach`, `Network`, `Resize`, `VotingMembership`, `NonVotingMembership`, `Attachment`, `Emergency`.

Tracker держит на `globalContext` несколько флагов / множеств (`hasMultiattachTransition`, `hasFormationTransition`, …) и применяет правила параллелизма из `CanAdmit`.

### 5.3. План EnableMultiattach

`images/controller/internal/controllers/rv_controller/datamesh/attachment_plans.go:107` регистрирует `enable-multiattach/v1` с группой `Multiattach` и единственной защитной проверкой `guardMaxAttachmentsAllowsMultiattach` (блокирует, когда `maxAttachments <= 1`).

### 5.4. Подписки rv-controller сегодня

`images/controller/internal/controllers/rv_controller/controller.go` сейчас подписан на: `ReplicatedVolume` (основной), `ReplicatedStorageClass`, `ReplicatedVolumeAttachment`, `ReplicatedVolumeReplica`, и `Owns(DRBDResourceOperation)`. Он НЕ подписан на `ReplicatedVolumeSnapshot` и НЕ подписан на другие `ReplicatedVolume` как клон-источники. Добавление двух подписок — обязательная предпосылка этого RFC.

## 6. Предлагаемый дизайн

### 6.1. Новый тип транзишна

В `api/v1alpha1/rv_types.go`:

```go
// ReplicatedVolumeDatameshTransitionTypeExternalReadInProgress означает,
// что у этого RV есть хотя бы один внешний потребитель (снапшот или
// чтение клон-источника), которому нужен ввод-вывод с одним primary.
// Пока этот транзишн активен, EnableMultiattach MUST NOT быть допущен.
ReplicatedVolumeDatameshTransitionTypeExternalReadInProgress ReplicatedVolumeDatameshTransitionType = "ExternalReadInProgress"
```

### 6.2. Новая группа Tracker'а

В `api/v1alpha1/rv_types.go`:

```go
// ReplicatedVolumeDatameshTransitionGroupExternalRead сериализует внешних
// читателей против переключения multiattach. Взаимоисключает
// EnableMultiattach.
ReplicatedVolumeDatameshTransitionGroupExternalRead ReplicatedVolumeDatameshTransitionGroup = "ExternalRead"
```

Группа сейчас содержит ровно один тип транзишна (`ExternalReadInProgress`). Она переиспользуется, если в будущем появятся другие виды «внешнего чтения».

### 6.3. План

`ExternalReadInProgress` — глобальный одношаговый план. Его единственная цель — существовать; состояние datamesh он не меняет. Шаги:

1. `Apply`: ничего не делает (возвращает `false` — состояние не менялось).
2. `Confirm`: подтверждается сразу. Транзишн остаётся активным, потому что `OnComplete` не вызывается, пока диспетчер не решит, что множество потребителей пусто (см. [§6.5](#65-жизненный-цикл-и-диспетчер)).

Регистрация плана (набросок, в `datamesh/external_read_plans.go`):

```go
func registerExternalReadPlans(reg *dmte.Registry[*globalContext, *ReplicaContext]) {
    extRead := reg.GlobalTransition(v1alpha1.ReplicatedVolumeDatameshTransitionTypeExternalReadInProgress)
    extRead.Plan("external-read-in-progress/v1").
        Group(v1alpha1.ReplicatedVolumeDatameshTransitionGroupExternalRead).
        DisplayName("External read in progress").
        Steps(
            dmte.GlobalStep("Hold", noopApply, confirmAlways),
        ).
        Build()
}
```

### 6.4. Защитная проверка на EnableMultiattach

В `attachment_plans.go` расширяем `enable-multiattach/v1`:

```go
enableMultiattach.Plan("enable-multiattach/v1").
    Group(v1alpha1.ReplicatedVolumeDatameshTransitionGroupMultiattach).
    DisplayName("Enabling multiattach").
    Guards(
        guardMaxAttachmentsAllowsMultiattach,
        guardNoExternalReadInProgress, // НОВЫЙ
    ).
    ...
```

`guardNoExternalReadInProgress` смотрит в `gctx.hasExternalReadTransition` (новый bool, поддерживаемый *Tracker*'ом в `Add`/`Remove` для группы `ExternalRead`). При блокировке возвращает:

```text
EnableMultiattach is blocked: ReplicatedVolume is being read by external consumers (snapshot or clone source); cannot enable multiattach until they finish.
```

Сообщение для пользователя MUST включать тип потребителя (`ReplicatedVolumeSnapshot <name>`, `ReplicatedVolume <clone-name> (clone)`), чтобы оператор знал, чего ждать или что отменить. Список рендерится из множества потребителей, хранящегося на `globalContext` (см. [§6.5](#65-жизненный-цикл-и-диспетчер)).

### 6.5. Жизненный цикл и диспетчер

`rv-controller` MUST добавить новый диспетчер (или расширить существующий), который:

1. Строит *множество потребителей* для RV, перечисляя две коллекции:
   - активные *`ReplicatedVolumeSnapshot`* со `spec.replicatedVolumeName == rv.Name` и `status.phase ∈ {Pending, InProgress}`;
   - целевые *`ReplicatedVolume`* в *Formation* со `spec.dataSource.Kind=ReplicatedVolume && spec.dataSource.Name == rv.Name`.
2. Если множество потребителей непусто И на `rv` нет активного `ExternalReadInProgress` → создаёт `CreateGlobalTransition(ExternalReadInProgress, "external-read-in-progress/v1")`.
3. Если множество потребителей пусто И активный `ExternalReadInProgress` есть → отменяет/завершает его (см. [§6.7](#67-семантика-завершения)).

Список потребителей MUST кэшироваться на `globalContext`, чтобы защитная проверка ([§6.4](#64-защитная-проверка-на-enablemultiattach)) и сообщения о прогрессе могли рендерить его без повторного запроса к API.

### 6.6. Новые подписки

В построитель `rv-controller` (`controller.go`) добавляем:

1. `Watches(&v1alpha1.ReplicatedVolumeSnapshot{}, EnqueueRequestsFromMapFunc(mapRVSToRV))` — отображает каждый RVS на его `spec.replicatedVolumeName`.
2. `Watches(&v1alpha1.ReplicatedVolume{}, EnqueueRequestsFromMapFunc(mapRVCloneTargetToSourceRV))` — отображает каждый RV со `spec.dataSource.Kind=ReplicatedVolume` на его `spec.dataSource.Name`. (Это подписка на тот же тип, который контроллер уже владеет; controller-runtime поддерживает такую вторичную подписку.)

Обе функции отображения SHOULD использовать индексированный список (расширить `images/controller/internal/indexes/`), чтобы не делать полный обход RV/RVS на каждое событие.

### 6.7. Семантика завершения

`ExternalReadInProgress` НЕ завершается автоматически. `Confirm` его единственного шага возвращает «всегда подтверждено», но завершение происходит явно: когда диспетчер видит пустое множество потребителей, он удаляет транзишн. (Аналогично `ForceDetach` и `CancelActiveOnCreate` мутируют состояние через API движка.)

Эквивалентно, план MAY использовать `Confirm`, который возвращает `MustConfirm` только когда множество пусто. Подход через диспетчер предпочтительнее, потому что он не размазывает логику множества потребителей по движку.

### 6.8. Сквозной флоу: создание RVS → завершение

Ниже — пошаговая трассировка штатного сценария «пользователь создаёт RVS, источник одно-primary, ничего не падает». В разделе показано, что происходит на каждом шаге и какой компонент это делает. Различия для клонирования RV→RV — в [§6.8.7](#687-различия-для-клонирования-rv-rv).

Условные обозначения акторов:

- `[user]` — внешний клиент (kubectl, оператор-человек, CSI-driver);
- `[apiserver]` — kube-apiserver;
- `[rv]` — `rv-controller` целиком;
- `[rvs]` — `rvs-controller`;
- `[rvr]` — `rvr-controller`;
- `[agent]` — `sds-replicated-volume-agent` на ноде, выполняет команды DRBD/LVM;
- `[engine]` — движок dmte внутри `[rv]`;
- `[tracker]` — *Tracker* в составе `[engine]`;
- `[dispatcher]` — диспетчер `ExternalReadInProgress`, новый компонент в `[rv]` ([§6.5](#65-жизненный-цикл-и-диспетчер)).

#### 6.8.1. Создание RVS

1. `[user]` выполняет `kubectl apply -f rvs.yaml` с `spec.replicatedVolumeName=src`.
2. `[apiserver]` сохраняет объект, рассылает `ADDED` подписчикам.

#### 6.8.2. Появление потребителя в rv-controller

3. Подписка `[rv]` на RVS ([§6.6](#66-новые-подписки)) через `mapRVSToRV` enqueue'ит реконсайл на `rv/src`.
4. `[rv]` начинает reconcile, читает `rv/src` из API, инициализирует `gctx`.
5. `[dispatcher]` строит множество потребителей по индексам:
   - RVS со `spec.replicatedVolumeName=src` и `status.phase ∈ {Pending, InProgress}` → `{rvs/snap-1}`;
   - RV со `spec.dataSource.Kind=ReplicatedVolume && spec.dataSource.Name=src` в Formation → `∅`.
   - Итог: `consumers = {rvs/snap-1}`.

#### 6.8.3. Создание ExternalReadInProgress

6. `[dispatcher]` видит непустое множество потребителей и отсутствие активного `ExternalReadInProgress` → вызывает API `[engine]` для создания глобального транзишна `external-read-in-progress/v1`.
7. `[engine]` запрашивает у `[tracker]` `CanAdmit(ExternalReadInProgress, group=ExternalRead)`. В группе `ExternalRead` пока нет других транзишнов; правил «не допускать одновременно с другой группой» для неё на этом этапе не объявлено → `Allow`.
8. `[engine]` записывает в `rv.Status.DatameshTransitions` новый элемент:

   ```yaml
   - type: ExternalReadInProgress
     group: ExternalRead
     planID: external-read-in-progress/v1
     steps:
       - name: Hold
         status: Active
         startedAt: <now>
   ```

9. `[engine]` обновляет состояние `[tracker]`: `gctx.hasExternalReadTransition=true`, плюс кэширует список потребителей в `gctx.externalReadConsumers` (для сообщений и защитных проверок).
10. `[engine]` исполняет `Apply` шага `Hold` — `noopApply` ничего не делает, возвращает `changed=false`. Затем `Confirm` — возвращает «подтверждено». Шаг помечается подтверждённым; транзишн остаётся `Active` (план не завершает его сам — завершит только `[dispatcher]`).
11. `[rv]` патчит `rv.Status` в `[apiserver]`. Реконсайл закрывается.

#### 6.8.4. rvs-controller снимает снимок (параллельно)

12. `[rvs]` реконсилит `rvs/snap-1` независимо от `[rv]`:
    - `reconcileMultiPrimary`: `rv.IsMultiPrimary()=false` → ветка отказа пропускается.
    - `reconcilePrepareMesh`: выбирает источниковую реплику, через `DRBDResourceOperation` поднимает `TrackBitmap` на primary, ждёт `bitmapSynced` на остальных diskful-членах. `phase=InProgress`.
13. `[agent]` исполняет DRBD-команды по `DRBDResourceOperation`-объектам; `[rvr]` обновляет статус реплик.
14. Когда мешка готова, `[rvs]` исполняет `reconcileSyncMesh`:
    - `SuspendIO` на primary;
    - `FlushBitmap` на всех diskful-членах;
    - LVM thin-snapshot на каждом diskful-узле через `[agent]`;
    - `ResumeIO` на primary;
    - дочерние RVRS создаются и переходят в `Ready`.
15. `[rvs]` ставит `phase=Completed`, `readyToUse=true`, патчит RVS.

На всём протяжении шагов 12–15 любая попытка `EnableMultiattach` для `rv/src` отклоняется ([§6.8.5](#685-параллельная-попытка-enablemultiattach-блокируется)).

#### 6.8.5. Параллельная попытка EnableMultiattach блокируется

Допустим, на шаге 14 `[user]` патчит `rv/src` `spec.maxAttachments=2` и создаёт второй `ReplicatedVolumeAttachment`.

16. `[rv]` реконсилит `rv/src`. Существующий dispatcher `Multiattach` ([§5.3](#53-план-enablemultiattach)) обнаруживает «multiattach должен быть включён» и пытается создать `EnableMultiattach`.
17. `[engine]` прогоняет Guards плана `enable-multiattach/v1`:
    - `guardMaxAttachmentsAllowsMultiattach` — `pass` (`maxAttachments=2`);
    - `guardNoExternalReadInProgress` — читает `gctx.hasExternalReadTransition=true` → `Block` с сообщением, перечисляющим потребителей из `gctx.externalReadConsumers` (`"rvs/snap-1"`).
18. `[engine]` НЕ создаёт `EnableMultiattach`. В `rv.Status.DatameshTransitions` записи о попытке нет. `[rv]` MAY поднять Kubernetes Event на `rv/src` с reason'ом `BlockedByExternalRead`.

Пока шаги 12–15 не завершились, реконсайлы `rv/src` повторяются (по таймеру или по новым событиям) и каждый раз приземляются на тот же `Block`.

#### 6.8.6. Снимок завершён, потребитель ушёл

19. `[apiserver]` рассылает `MODIFIED` для RVS (фаза стала `Completed`). Mapper из шага 3 enqueue'ит реконсайл на `rv/src`.
20. `[rv]` реконсилит. `[dispatcher]` строит множество потребителей:
    - RVS с `phase=Completed` НЕ попадает в `{Pending, InProgress}` → `∅`;
    - клон-target'ов нет → `∅`.
    - Итог: `consumers = ∅`.
21. `[dispatcher]` видит активный `ExternalReadInProgress` и пустое множество → вызывает API `[engine]` для отмены транзишна.
22. `[engine]` помечает шаг `Hold` и сам транзишн как `Completed`. На этом или следующем реконсайле `[engine]` удаляет завершённый транзишн из `rv.Status.DatameshTransitions` по обычным правилам очистки dmte.
23. `[engine]` обновляет `gctx.hasExternalReadTransition=false`, очищает `gctx.externalReadConsumers`.
24. `[rv]` патчит статус.

Если на шаге 19 в реконсайле также присутствует ожидающий `EnableMultiattach` (пользователь продолжает требовать второй attach), он пройдёт на следующих фазах того же или следующего цикла:

25. `[engine]` сначала исполняет фазу `settle` — закрывает `ExternalReadInProgress`.
26. На фазе `dispatch` снова прогоняются Guards `enable-multiattach/v1`. Теперь `gctx.hasExternalReadTransition=false` → `guardNoExternalReadInProgress` пропускает; `[tracker]`.`CanAdmit` пропускает.
27. `EnableMultiattach` создаётся, его план запускается штатно.

Между шагом 15 (RVS `Completed`) и шагом 27 (`EnableMultiattach` создан) — обычно один-два цикла реконсайла, миллисекунды.

#### 6.8.7. Различия для клонирования RV→RV

Флоу клона симметричен; меняются триггер и работа на стороне источника:

- Шаги 1–2: `[user]` создаёт `rv/clone` со `spec.dataSource.Kind=ReplicatedVolume, Name=src`. `[apiserver]` рассылает `ADDED`.
- Шаг 3: подписка `[rv]` на `ReplicatedVolume` через `mapRVCloneTargetToSourceRV` enqueue'ит реконсайл на `rv/src`.
- Шаг 5: `[dispatcher]` находит `rv/clone` среди клон-target'ов в Formation → `consumers = {rv/clone (clone)}`.
- Шаги 6–11: создание `ExternalReadInProgress` на `rv/src` идентично RVS-флоу.
- Шаги 12–15 заменяются на работу `[rv]` `reconcileFormation` на `rv/clone`: LVM thin-clone на primary-узле источника, DRBD initial sync для не-primary членов клона. По завершении транзишн `Formation` на `rv/clone` уходит в `Completed` и удаляется.
- Шаги 19–24: `[dispatcher]` на `rv/src` видит, что `rv/clone` больше не в Formation → отменяет `ExternalReadInProgress`.

Финализатор `RVCloneSourceFinalizer` (см. [§4](#4-не-цели), пункт 2) живёт _параллельно_ с `ExternalReadInProgress` и снимается по тому же триггеру (Formation у `rv/clone` завершён). Они не пересекаются логически: финализатор охраняет жизненный цикл объекта `rv/src`, `ExternalReadInProgress` — его топологию.

### 6.9. Анализ гонок по этапам жизненного цикла

Жизненный цикл `ExternalReadInProgress` распадается на пять этапов. Для каждого этапа ниже перечислены возможные гонки и средства защиты.

```
Этап 1                Этап 2                  Этап 3                Этап 4               Этап 5
появление    →   создание ExternalRead  →    жизнь        →    завершение       →    удаление
потребителя       (диспетчер)             ExternalRead         потребителя       ExternalRead
```

#### 6.9.1. Этап 1: появление потребителя

**Гонка 1.1: RVS / клон-target создан, но `rv-controller` ещё не получил событие подписки.**

В этом окне `EnableMultiattach` MAY быть допущен, потому что множество потребителей на `globalContext` ещё пусто. Окно ограничено латентностью watch-события (миллисекунды).

Защита:

- Двухуровневая. Нижний уровень — `reconcileMultiPrimary` в `rvs-controller` ([§6.10](#610-миграция-и-совместимость), пункт 3): если несколько primary всё-таки случилось, RVS падает в `Failed` с понятным сообщением.
- Верхний уровень — `rv-controller` MUST реконсилить RV при каждом событии подписки на RVS / клон-target ([§6.6](#66-новые-подписки)). Окно гарантированно закроется на следующем reconcile, и любой следующий `EnableMultiattach` будет отклонён.

**Гонка 1.2: одновременно создаются два потребителя одного RV (два RVS, или RVS и клон-target).**

Защита: множество потребителей — это просто список объектов; диспетчер агрегирует его на каждом реконсайле. `ExternalReadInProgress` создаётся ровно один раз и не дублируется (см. этап 2).

#### 6.9.2. Этап 2: создание ExternalReadInProgress

**Гонка 2.1: множество потребителей и попытка `EnableMultiattach` появляются в одном реконсайле.**

Например, пользователь патчит `maxAttachments=2` одновременно с появлением RVS.

Защита: внутри одного цикла `rv-controller` сначала прогоняет диспетчеры, и наш диспетчер создаёт `ExternalReadInProgress` _до_ того, как диспетчер `EnableMultiattach` успевает запросить admission. Защитная проверка `guardNoExternalReadInProgress` блокирует создание `EnableMultiattach`. Это главный устойчивый путь корректности.

**Гонка 2.2: два параллельных реконсайла одного RV.**

`rv-controller` использует один основной reconcile per RV (`MaxConcurrentReconciles=10` распределяется между _разными_ RV). Параллельно тот же RV не реконсилится. Любая попытка двойного создания `ExternalReadInProgress` дополнительно отлавливается *Tracker*'ом (группа `ExternalRead`, `CanAdmit` запретит второй транзишн той же группы).

**Гонка 2.3: `rv-controller` рестартует во время реконсайла, `ExternalReadInProgress` уже создан, но потребитель уже исчез.**

После рестарта `rv-controller` строит множество потребителей с нуля. Если оно пустое, диспетчер на ближайшем реконсайле удаляет «осиротевший» `ExternalReadInProgress`. Никаких ручных действий не требуется.

#### 6.9.3. Этап 3: жизнь ExternalReadInProgress

**Гонка 3.1: попытка `EnableMultiattach` пока `ExternalReadInProgress` активен.**

Защита: защитная проверка `guardNoExternalReadInProgress` ([§6.4](#64-защитная-проверка-на-enablemultiattach)) синхронно блокирует создание; в `rv.Status.DatameshTransitions` запись о `EnableMultiattach` не появляется.

**Гонка 3.2: уже идущий `EnableMultiattach` (admitted до появления потребителя) и появление потребителя.**

Это вырожденный случай гонки 1.1. `EnableMultiattach` шага `Apply` уже выполнил, состояние с несколькими primary становится фактическим. `ExternalReadInProgress` всё равно создаётся, но он не откатывает уже применённое.

Защита: см. гонку 1.1 — RVS / клон фиксирует факт нескольких primary через `reconcileMultiPrimary` или `rv.IsMultiPrimary()` и завершается ошибкой. Полностью предотвратить гонку 3.2 без распределённого лока на admission в API-сервере (т. е. webhook) невозможно; принципиально допускаем её, потому что:

- `EnableMultiattach` — операция оператора (set `maxAttachments=2` + второй `Attach`), не автоматическая;
- падение RVS в `Failed` — обратимо: оператор удаляет RVS и пробует снова после возврата к одному primary.

**Гонка 3.3: пользователь форсированно удаляет `ExternalReadInProgress` через `kubectl edit`.**

Технически возможно, потому что `status.datameshTransitions` — обычное поле статуса. На следующем реконсайле диспетчер увидит непустое множество потребителей и пересоздаст `ExternalReadInProgress`. В промежутке окно равно одному циклу реконсайла. Документация SHOULD предупреждать, что ручное редактирование транзишнов — диагностический инструмент, не штатный.

#### 6.9.4. Этап 4: завершение потребителя

**Гонка 4.1: RVS перешёл в `Completed`/`Failed`, событие подписки ещё не дошло до `rv-controller`.**

`ExternalReadInProgress` остаётся активным, `EnableMultiattach` всё ещё блокируется. Защита: задержка ограничена латентностью watch-события; на следующем реконсайле диспетчер удалит `ExternalReadInProgress`.

**Гонка 4.2: один потребитель завершился, второй сохранился.**

Множество потребителей строится на каждом реконсайле; если хотя бы один остался, `ExternalReadInProgress` остаётся активным. Никакой специальной защиты не требуется.

**Гонка 4.3: клон-target удалён до завершения формирования.**

Клон-target пропадает из множества потребителей. Если других потребителей нет — диспетчер удаляет `ExternalReadInProgress`. Финализатор `RVCloneSourceFinalizer` обрабатывается отдельной логикой (см. [§4](#4-не-цели), пункт 2) и не пересекается.

**Гонка 4.4: потребитель завершился, но потом сразу появился новый (тот же клиент пересоздаёт RVS).**

Между двумя событиями диспетчер _может_ успеть удалить `ExternalReadInProgress` и потом создать его заново. В этом окне `EnableMultiattach` MAY быть допущен (это гонка 1.1, эквивалент в обратную сторону). Окно — миллисекунды.

Защита: та же двухуровневая, что и в гонке 1.1. На практике сценарий маловероятен — `kubectl delete rvs ... && kubectl apply ...` всё равно проходит через API-сервер с задержками, а диспетчер реконсилит RV не реже, чем приходят события.

#### 6.9.5. Этап 5: удаление ExternalReadInProgress

**Гонка 5.1: `rv-controller` патчит `status.datameshTransitions` (удаляя `ExternalReadInProgress`), параллельно другой клиент патчит тот же RV.**

Защита: optimistic concurrency на `resourceVersion` API-сервера. Если кто-то изменил RV между чтением и patch'ем, patch вернёт `Conflict`, controller-runtime автоматически переочередит реконсайл, диспетчер снова прочитает множество потребителей и снова решит, что делать.

**Гонка 5.2: `ExternalReadInProgress` удалён, в том же реконсайле появился новый потребитель.**

Диспетчер запускается _до_ финального patch'а статуса; если новый потребитель попал в реконсайл (через подписку, инициировавшую этот цикл), диспетчер создаст `ExternalReadInProgress` заново в том же цикле. Если потребитель появился в API между текущим и следующим реконсайлом — закроется на следующем reconcile (гонка 1.1).

**Гонка 5.3: удаление `ExternalReadInProgress` и допуск `EnableMultiattach` в одном реконсайле.**

Это штатный путь: потребитель ушёл, оператор хочет multiattach. Внутри одного цикла движок dmte сначала выполняет фазу `settle` (где `ExternalReadInProgress` удаляется), потом фазу `dispatch` (где `EnableMultiattach` создаётся). Порядок гарантирован движком; никакой защиты сверх этого не требуется.

#### 6.9.6. Сводка по защите

| Этап | Гонка | Защита | Допустимое окно |
|---|---|---|---|
| 1 | 1.1 RVS/клон создан, событие не дошло | watch + двухуровневый отказ через `reconcileMultiPrimary` | мс |
| 1 | 1.2 Два потребителя одновременно | агрегация множества на каждом реконсайле | — |
| 2 | 2.1 Потребитель + `EnableMultiattach` в одном цикле | порядок диспетчеров + защитная проверка | — |
| 2 | 2.2 Параллельные реконсайлы | per-RV serialization + *Tracker* | — |
| 2 | 2.3 Рестарт `rv-controller` | состояние пересобирается из API; осиротевший транзишн удаляется | один цикл реконсайла |
| 3 | 3.1 `EnableMultiattach` при активном `ExternalReadInProgress` | синхронная защитная проверка | 0 |
| 3 | 3.2 Уже идущий `EnableMultiattach` | вырождение 1.1; отказ через `reconcileMultiPrimary` | мс |
| 3 | 3.3 Ручное удаление `ExternalReadInProgress` через `kubectl` | пересоздание на следующем реконсайле | один цикл реконсайла |
| 4 | 4.1 Потребитель завершился, событие не дошло | отложенное удаление на следующем реконсайле | мс |
| 4 | 4.2 Частичное завершение | агрегация множества | — |
| 4 | 4.3 Клон-target удалён до Formation | агрегация множества; финализатор клона отдельно | — |
| 4 | 4.4 Re-create потребителя | вырождение 1.1; то же средство защиты | мс |
| 5 | 5.1 Конфликт patch'а статуса | optimistic concurrency + автоматическая повторная очередь | — |
| 5 | 5.2 Новый потребитель в момент удаления | агрегация множества в том же цикле или на следующем | один цикл реконсайла |
| 5 | 5.3 Удаление `ExternalReadInProgress` + `EnableMultiattach` в одном цикле | порядок dmte: `settle` → `dispatch` | — |

Все остающиеся окна — миллисекундные и сводятся к гонке 1.1; нижний уровень защиты (`reconcileMultiPrimary` в `rvs-controller`) гарантирует консистентность ценой возможного `Failed` у RVS / клона.

### 6.10. Миграция и совместимость

1. Добавление нового `TransitionType` и `TransitionGroup` — добавочное изменение API; старые контроллеры корректно игнорируют неизвестные типы и группы.
2. Старые контроллеры, работающие против новых RV, увидят `ExternalReadInProgress` в `status.datameshTransitions`, но не поймут его. Они MUST трактовать неизвестные транзишны как непрозрачные (текущий реестр dmte уже так делает; покрыть тестом).
3. Существующий `reconcileMultiPrimary` в `rvs-controller` SHOULD быть сохранён как страховочная сеть на время раскатки (см. гонки 1.1 и 3.2 в [§6.9](#69-анализ-гонок-по-этапам-жизненного-цикла)). Удалить его MAY после того, как новый транзишн достаточно долго проживёт в продакшене.
4. Миграция схемы CRD не требуется.

### 6.11. Наблюдаемость

Новый транзишн виден в `kubectl get rv -o yaml` под `status.datameshTransitions`. Его сообщение MUST перечислять активных потребителей, чтобы оператор мог точно понять, кого ждать или кого отменить:

```yaml
status:
  datameshTransitions:
  - group: ExternalRead
    planID: external-read-in-progress/v1
    type: ExternalReadInProgress
    steps:
    - name: Hold
      status: Active
      message: "Holding for 2 consumers: rvs/snap-1, rv/clone-7 (clone)"
      startedAt: "..."
```

## 7. Вне области и продолжение

1. Восстановление из RVS (`Kind=ReplicatedVolumeSnapshot`) — вне области. Восстановление читает RVRS, а не источник-RV.
2. Кросс-кластерная репликация, если появится в будущем, станет третьим видом «внешнего читателя». Группа и транзишн спроектированы так, чтобы поглотить такие случаи без правок `EnableMultiattach`.
3. Сквозные тесты:
   - источник становится целью RVS → на RV появляется `ExternalReadInProgress`;
   - пока RVS в `InProgress`, попытка `EnableMultiattach` (например, поставить `maxAttachments=2` и создать второй RVA) — блокируется ожидаемым сообщением;
   - RVS завершён или удалён → `ExternalReadInProgress` пропадает; `EnableMultiattach` проходит;
   - тот же сценарий с клоном RV→RV вместо RVS.

## 8. Открытые вопросы

1. Должен ли `ExternalReadInProgress` блокировать также другие опасные транзишны (например, `Detach` последнего primary)? Сегодня конвейер снапшота терпит смену primary; клон может не терпеть. Решать отдельно для каждого потребителя.
2. Стоит ли отражать жизненный цикл транзишна на RVS / целевом клоне как условие (`SourceReady=True`)? Полезно пользователям, но повышает связанность. Отложить.

## Ссылки

- [RFC2119] Bradner, S., "Key words for use in RFCs to Indicate Requirement Levels", BCP 14, RFC 2119, March 1997.
- [RFC8174] Leiba, B., "Ambiguity of Uppercase vs Lowercase in RFC 2119 Key Words", BCP 14, RFC 8174, May 2017.
- `images/controller/internal/controllers/rv_controller/dmte/README.md`
- `images/controller/internal/controllers/rv_controller/datamesh/attachment_plans.go`
- `images/controller/internal/controllers/rv_controller/datamesh/concurrency_tracker.go`
- `images/controller/internal/controllers/rvs_controller/reconciler.go` (`reconcileMultiPrimary`)
- `images/controller/internal/controllers/rv_controller/reconciler_clone_source.go`
