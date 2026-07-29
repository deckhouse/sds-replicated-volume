# Подходы к административному локу DRBD во время снятия снапшота и клонирования

Статус: гибрид A+B реализован (см. [§Текущее состояние реализации](#текущее-состояние-реализации)). Этот документ остался как обзор альтернатив и обоснование выбора.

## Контекст

Во время снятия снапшота `ReplicatedVolumeSnapshot` (RVS) и во время клонирования `ReplicatedVolume`→`ReplicatedVolume` ниодин внешний агент не должен менять топологию или роль источника-RV: иначе образ окажется неконсистентным. Сегодня этой защиты у нас нет — попытки рассмотрены в [docs/dev/external-read-in-progress-rfc.md](external-read-in-progress-rfc.md) на уровне datamesh-транзишнов. Этот документ обсуждает альтернативный, более низкоуровневый путь: ставить «лок» на сами DRBD-ресурсы.

Ниже — два архитектурных подхода. По каждому: название, путь реализации, сквозной флоу, плюсы, опасности, как мы их адресуем. В конце — сводное сравнение, гибридный путь и открытые вопросы.

## Подход A: control-plane lock через DRBDResourceOperation

### A.1. Название

Control-plane lock через расширение `DRBDResourceOperation` (DRBDOp) новым типом `LockAdmin`. Лок per-DRBDResource (= per-нода-реплика); оркестрация N локов лежит на caller'е.

### A.2. Путь реализации

Сущности и места изменений:

- API: новый константный тип `DRBDResourceOperationLockAdmin` в [api/v1alpha1/drbdrop_types.go](../../api/v1alpha1/drbdrop_types.go), расширение `kubebuilder:validation:Enum` для `Spec.Type`.
- Агент на ноде: новый метод `reconcileLockAdmin` в [images/agent/internal/controllers/drbdrop/reconciler.go](../../images/agent/internal/controllers/drbdrop/reconciler.go); pre-gate в обработке остальных admin-DRBDOps; явный whitelist snapshot/IO-операций (`SuspendIO`, `ResumeIO`, `FlushBitmap`, `TrackBitmap`, `UntrackBitmap`).
- Caller для снапшота (rvs-controller): новый шаг перед `reconcilePrepareMesh` в [images/controller/internal/controllers/rvs_controller/reconciler.go](../../images/controller/internal/controllers/rvs_controller/reconciler.go) — перечисляет RVRs, создаёт N DRBDOp с детерминированными именами, ждёт `Phase=Running` на всех.
- Caller для клона (rv-controller клон-флоу): аналогичный шаг в [images/controller/internal/controllers/rv_controller/reconciler_clone_source.go](../../images/controller/internal/controllers/rv_controller/reconciler_clone_source.go) перед формированием клон-таргета.
- Cleanup: `OwnerReference` от RVS / клон-таргет-RV на каждый LockAdmin DRBDOp → kubernetes GC снимает; плюс явное удаление при terminal phase у caller'а.

Архитектурно: лок — это набор DRBDOp-объектов в etcd, по одному на каждую реплику. Агенты на нодах их видят и пресекают новые admin-DRBDOps на свои локальные ресурсы.

### A.3. Сквозной флоу

Сценарий — создание RVS:

1. `[user]` создаёт RVS со `spec.replicatedVolumeName=src`.
2. `[rvs]` реконсилит RVS: перечисляет N RVRs RV `src` → создаёт N `DRBDOp{Type: LockAdmin, DRBDResourceName: rvrName, ownerRef: RVS}`. Имена детерминированы: `<rvs.uid>-lock-<rvrName>`.
3. На каждой ноде `[agent]` принимает свой LockAdmin: ждёт, пока завершатся уже выполняющиеся admin-DRBDOps на этом ресурсе → помечает локальный DRBDResource как «locked» во внутренней структуре агента → ставит `Phase=Running` на DRBDOp.
4. `[rvs]` дожидается, что ВСЕ N локов в `Running`. Только после этого приступает к снапшоту.
5. `[rvs]` запускает обычный `reconcilePrepareMesh` / `reconcileSyncMesh` — `TrackBitmap`, `FlushBitmap`, `SuspendIO`, LVM thin-snapshot, `ResumeIO`. Эти DRBDOps в whitelist'е и проходят сквозь lock.
6. RVS переходит в `Completed`.
7. `[rvs]` удаляет все N LockAdmin DRBDOps. Если `[rvs]` упал раньше — owner-GC снимет при удалении RVS.

Сценарий — клонирование RV→RV: ровно те же шаги, но holder лока — `[rv]`, ownerRef — клон-таргет, лок берётся перед LVM thin-clone и снимается после Formation.

### A.4. Положительные стороны

- Time-to-prototype 2-4 дня календарных.
- Знакомый стек: Go, controller-runtime, k8s API. Никаких C/kernel.
- Никаких изменений DRBD-kernel-модуля и wire-протокола.
- Гибкость семантики: легко добавлять политики, conditions, аннотации holder'а, диагностические сообщения.
- `OwnerReference` = автоматическая GC при смерти владельца, без отдельной liveness-инфраструктуры.
- Отладка через `kubectl get/describe/events`, аудит API-сервера «бесплатно».
- RBAC-контроль: кто может создавать LockAdmin — на уровне k8s.
- Никак не накладывается на потенциальных не-k8s пользователей нашего DRBD-модуля.
- Эволюционируемо: «прозрачное» добавление kernel-уровня позже не меняет контракт наружу.

### A.5. Опасности

1. **Обходимо вручную.** Оператор по ssh `drbdadm primary` минует гейт — наш агент в этом не участвует.
2. **Race-окно от создания DRBDOp до Running на всех N узлах.** Между «caller сделал create» и «все N агентов подтвердили» может прилететь admin-DRBDOp; на тех узлах, где лок ещё не активен, он проскочит.
3. **Корректность оркестрации лежит на caller'е.** Bug в rvs-controller / rv-controller (не все N взял, не все снял) → orphan-локи или незащищённый снапшот.
4. **Поведение при network split.** Если ноды отрезаны от API-сервера и не получают LockAdmin вовремя — а на эти ноды локально кто-то поднимает admin-action — лок не сработает.
5. **Несогласованность лока с реальным DRBD-state.** Сам факт того, что у нас есть LockAdmin DRBDOp в `Running`, не гарантирует, что DRBD прямо сейчас не получает admin-команду по другому каналу.
6. **Множественность объектов.** N локов на каждый снапшот — для сотен снапшотов в день нагрузка на etcd-watch'и ощутима.

### A.6. Как мы их решаем

1. **Обход.** В Phase 0 — документация: «не трогать руками во время снапшота». Если ssh-обход станет реальной проблемой в продакшене — добавляем Phase 1 (kernel-lock под капотом нашего агента); контракт наружу не меняется. См. [§Возможный гибрид](#возможный-гибрид).
2. **Race-окно.** Caller MUST не приступать к снапшоту, пока ВСЕ N локов не `Running`. Это invariant в коде, покрытый юнит-тестом. Параллельный снапшот, оказавшийся в гонке, обнаружит, что предыдущий лок ещё не отпущен, и встанет в очередь на агенте.
3. **Корректность caller'а.** Детерминированные имена DRBDOps по `(holder.uid, rvrName)` — рестарт caller'а не плодит дубликаты. Owner-ref от RVS/RV на DRBDOp — мусор не остаётся даже при abnormal termination. Юнит-тесты на пути «caller упал между N1 и N2 взятыми локами», «caller рестартнул, нашёл свои локи».
4. **Network split.** На отрезанной от API ноде агент не получает событий ВООБЩЕ — а значит и admin-DRBDOps туда тоже не доходят. Окно симметрично: либо оба видны, либо оба нет. Защита сохраняется.
5. **Несогласованность с DRBD-state.** Принимаем как ограничение control-plane уровня. Если нужна жёсткая гарантия — Phase 1 с kernel-локом.
6. **Шум etcd.** Замерить на e2e; если станет проблемой — упаковать N локов в один CR `DRBDResourceLock` с массивом ресурсов в `spec` (промежуточная Phase 0.5).

## Подход B: cluster-wide kernel lock через twopc

### B.1. Название

Cluster-wide kernel lock через новые netlink-команды `DRBD_ADM_LOCK` / `DRBD_ADM_UNLOCK`, распространяющиеся существующим two-phase-commit-протоколом (twopc) DRBD.

### B.2. Путь реализации

Сущности и места изменений:

- Kernel: добавить `bool admin_locked` (или richer struct с holder-info) в `struct drbd_resource`. Добавить новые netlink-команды в [3p-drbd-headers/linux/drbd_genl.h](../../../3p-drbd-headers/linux/drbd_genl.h) и handler'ы в [3p-drbd-old/drbd/drbd_nl.c](../../../3p-drbd-old/drbd/drbd_nl.c). Использовать существующий twopc-механизм (см. `change_cluster_wide_state()` в [3p-drbd-old/drbd/drbd_state.c](../../../3p-drbd-old/drbd/drbd_state.c) около строки 4920) — новый `change_type=ADMIN_LOCK` поверх существующих `P_TWOPC_PREPARE`/`P_TWOPC_COMMIT`/`P_TWOPC_ABORT`.
- Userspace: добавить парсер новой команды и подкоманды `lock` / `unlock` в [3p-drbd-utils/user/v9/drbdsetup.c](../../../3p-drbd-utils/user/v9/drbdsetup.c) и [3p-drbd-utils/user/v9/drbdsetup_main.c](../../../3p-drbd-utils/user/v9/drbdsetup_main.c).
- Gate в admin handler'ах: в начало каждого `drbd_adm_*` handler'а (кроме whitelist'а snapshot/IO-операций) добавить `if (resource->admin_locked) return -EBUSY;`.
- Lock holder identification: привязать к open netlink-сокету (как `flock(2)` к fd), `release()` сокета → kernel снимает; альтернатива — TTL + renew от userspace.
- Recovery после reconnect: lock-state синкается в составе обычного resync-handshake'а DRBD.
- Caller (наш агент): дёргает новую команду `drbdsetup lock <resource>` на одном из узлов; ядро через twopc распространяет на все. Освобождение — `drbdsetup unlock` или `close()` сокета.

Архитектурно: лок — нативное свойство DRBD-resource, реплицируется между узлами тем же механизмом, что и роль primary/secondary, attach/detach и т.д.

### B.3. Сквозной флоу

Сценарий — создание RVS:

1. `[user]` создаёт RVS со `spec.replicatedVolumeName=src`.
2. `[rvs]` через нашего `[agent]` делает netlink-вызов `DRBD_ADM_LOCK` на любом доступном узле RV.
3. Kernel инициирует twopc: `P_TWOPC_PREPARE` с `change_type=ADMIN_LOCK` всем достижимым узлам RV.
4. На каждом узле kernel проверяет, что нет inflight admin-операции на этот resource → отвечает `P_TWOPC_YES`. Если есть — `P_TWOPC_NO`, координатор повторяет с backoff.
5. При всех YES координатор шлёт `P_TWOPC_COMMIT`. Каждый узел выставляет `resource->admin_locked=true` под `state_mutex`.
6. Координатор возвращает success в userspace вызывающему.
7. `[rvs]` получает success → запускает snapshot-флоу: `SuspendIO` / `FlushBitmap` / LVM thin-snapshot / `ResumeIO`. Snapshot-операции в whitelist'е, проходят сквозь lock.
8. После завершения snapshot'а `[rvs]` вызывает `DRBD_ADM_UNLOCK` (или просто `close()` сокета, если lock привязан к fd) → новый twopc → все узлы снимают `admin_locked`.
9. RVS переходит в `Completed`.

Сценарий — клонирование RV→RV: identical, holder — клон-флоу `[rv]`, лок берётся перед LVM thin-clone и снимается после Formation.

### B.4. Положительные стороны

- **Защита от обхода.** ssh + `drbdadm primary` упирается в `-EBUSY` на уровне netlink. Гарантия не зависит от вежливости оператора.
- **Атомарность.** Twopc = либо лок взят на ВСЕХ, либо ни на ком. Никакого окна неконсистентности.
- **Один авторитативный источник правды.** Лок живёт там же, где state DRBD-ресурса, синхронизируется тем же механизмом.
- **Самосогласованность.** Lock и admin-операции держатся на одних и тех же mutex'ах; нет TOCTOU.
- **Caller получает результат сразу.** Вернулся success — лок реально стоит везде. Не нужно опрашивать N статусов.
- **Recovery при reconnect — встроен.** Когда отвалившийся узел подключается, lock-state приходит вместе с обычным resync.
- **Простой контракт для caller'а.** Один вызов `drbdsetup lock` — один результат. Никакой оркестрации N объектов.

### B.5. Опасности

1. **Стоимость разработки.** ~3-5 недель календарных — kernel + drbd-utils + multi-node стенд + регрессии.
2. **Backward compatibility.** Если в кластере смешаны ноды с разными версиями kernel-модуля (в момент rolling upgrade) — старые не понимают новый twopc-тип, шлют `P_TWOPC_NO`, лок взять невозможно вообще.
3. **Partition behavior.** Что делать, если кластер расколот на minority/majority в момент попытки lock'а?
4. **Lock owner и его смерть.** Initiator упал, лок остался на всех узлах.
5. **Blast radius bug'ов.** Bug в координации lock'а = потенциальный стопор кластера (например, COMMIT не дошёл, ноды в half-locked-состоянии).
6. **Тестирование требует multi-node стенда.** Plus partition-injection, reconnect-сценарии.
7. **Семантика inflight admin-операции.** Если в момент `lock` уже бежит `set_role` под `state_mutex` — lock ждёт или фейлит?
8. **Менее знакомая команде область.** Меньше людей могут компетентно ревьюить kernel-патч.
9. **Сложнее observability.** Состояние лока живёт в kernel; видимо только через `drbdsetup status` или `debugfs`, не через `kubectl`.
10. **Сложнее RBAC.** Lock берётся под `CAP_SYS_ADMIN` на ноде; нет тонкой разграничивающей политики на уровне самой команды.

### B.6. Как мы их решаем

1. **Стоимость.** Принимаем как осознанную инвестицию. Делаем поэтапно: сначала прототип на одной ноде (без twopc, просто per-node флаг и netlink-команды) → потом twopc-расширение. Каждая стадия независимо ценна.
2. **Backward compat.** Feature flag через `protocol_min_version` в DRBD-handshake. Узлы заявляют поддержку lock'а; если не все умеют — `drbdsetup lock` возвращает «not all peers support admin-lock». Во время rolling upgrade операторы знают, что снапшоты временно недоступны.
3. **Partition.** Решение по аналогии с тем, как primary требует quorum: lock требует quorum. Minority partition не может ни взять, ни снять. Это явно прописывается и тестируется.
4. **Lock owner.** Базовый паттерн — lock привязан к open netlink-сокету. Когда сокет закрывается (умер процесс, reboot ноды), kernel снимает. Альтернативно (или дополнительно) — TTL с обязательным renew. Документируется выбор.
5. **Blast radius.** Стандартные практики: код-ревью kernel-разработчиком (внутренним или контрактным), canary-rollout (сначала на не-критичные кластеры), kdump / crashdump для post-mortem, явный escape hatch через CAP_SYS_ADMIN-команду «force-unlock».
6. **Тестирование.** Стенд из 3-5 KVM-нод. Injection-тесты с искусственным partition (`iptables DROP` на DRBD-портах). Chaos-набор: «взять лок, убить координатора, проверить recovery». Это надо построить, и это часть стоимости разработки.
7. **Inflight semantics.** По аналогии с обычными DRBD admin-операциями — lock ждёт `state_mutex`. Документируется. Опционально команда принимает `--timeout`.
8. **Знание области.** Привлекаем эксперта по kernel-разработке для ревью первых версий. После пары успешных PR'ов команда подтягивается.
9. **Observability.** Параллельно с kernel-патчем — поле `lockHolder` в `DRBDResource.Status`, которое наш агент периодически синкает из `drbdsetup status`. Тогда `kubectl describe drbdresource` показывает текущий лок.
10. **RBAC.** Принимается. На уровне k8s оборачиваем kernel-вызов в наш агент; агент делает RBAC-проверку перед netlink-вызовом. Гранулярность — через k8s, физическая защита — через kernel.

## Текущее состояние реализации

Реализован гибрид A + B Phase 2: kernel-level cluster-wide lock через twopc, обёрнутый в `DRBDResourceOperation` и оркестрируемый Kubernetes-контроллером. Caller (RVS controller) видит **один** CR на снапшот, а не N — атомарность обеспечена ядром через twopc.

### Текущая архитектура

#### Kernel (DRBD ≥ 9.next)

- `DRBD_FF_ADMIN_LOCK` feature flag: новый кластер-вайд `twopc` change `TWOPC_ADMIN_LOCK` (см. `3p-drbd-old/drbd/drbd_state.c`, `drbd_nl.c`, `drbd_protocol.h`).
- Resource-level флаг `admin_locked` + `admin_lock_holder_node_id` + `admin_lock_generation` (TWOPC tid).
- Гейт в начале каждого `drbd_adm_*` handler: если `admin_locked` и команда не из IO-orchestration whitelist (`SuspendIO`, `ResumeIO`, `NewCurrentUUID`, `TrackBitmap`, `FlushBitmap`, lock/unlock/force-unlock) — возврат `ERR_LOCK_HELD`.
- Owner-id привязан к `genl_family` сокету, который держит lock — close сокета → `ForceUnlock` через twopc.
- `DRBD_NLA_RESOURCE_OPTS.admin_lock_wait_timeout` — per-resource опция для `wait_for_local_drain`.

#### Userspace `drbdsetup`

- Подкоманды `lock <resource>`, `unlock <resource>`, `force-unlock <resource>` — см. `3p-drbd-utils/user/v9/drbdsetup.c` и `drbdsetup_main.c`.
- Возвращаемые exit-коды: `ERR_LOCK_*` (176-180) → exit 10. См. `images/agent/pkg/drbdutils/commands.go` (`ExecuteLock`/`ExecuteUnlock`/`ExecuteForceUnlock`) — мапим в Go-ошибки `ErrLockHeld`, `ErrNotLockHolder`, `ErrLockBusy`, `ErrLockNotHeld`, `ErrLockNotSupported`.

#### Kubernetes API

- `DRBDResourceOperation` types: `LockAdmin`, `ForceUnlockAdmin` (см. `api/v1alpha1/drbdrop_types.go`).
- `DRBDResource.Status.AdminLock` (`Held`, `HolderNodeID`, `Generation`) — экспортируется агентом из `drbdsetup status --json`.
- Finalizer на `DRBDOp{Type=LockAdmin}`: `drbd.deckhouse.io/admin-lock` — гарантирует, что при `Delete` агент успеет вызвать `unlock`/`force-unlock` до GC.
- Condition на RVS: `AdminLocked` (см. `api/v1alpha1/rvs_conditions.go`) с reasons `Acquired`/`Acquiring`/`ClusterNotReady`/`HolderUnready`/`NoHolder`/`Released`/`Releasing`/`RetryingTransient`.

#### Агент (`drbdrop` controller)

- `reconcileLockAdmin`: вызывает `ExecuteLock`, ставит `Phase=Running`, добавляет `AdminLockFinalizer`. На `ErrLockHeld`/`ErrLockBusy` ставит `Phase=Failed` (caller инициирует retry).
- `reconcileForceUnlockAdmin`: вызывает `ExecuteForceUnlock` — escape hatch для админа.
- При `Delete` `LockAdmin` op'а с finalizer: вызывает `ExecuteUnlock`; на `ErrNotLockHolder`/`ErrLockNotHeld` снимает finalizer (idempotent). Predicates пропускают объекты в terminal phase, если у них есть `DeletionTimestamp` + finalizer.
- Покрыто unit-тестами в `images/agent/internal/controllers/drbdrop/admin_lock_test.go`.

#### Caller — RVS controller

Реализация: `images/controller/internal/controllers/rvs_controller/reconciler_admin_lock.go`. Покрытие — `reconciler_admin_lock_test.go`.

Жизненный цикл lock'а внутри `Reconcile`:

1. **Acquire** (вызывается перед `reconcilePrepareMesh`/`reconcileSyncMesh`):
   - `getAdminLockOp`: ищем существующий `LockAdmin` DRBDOp по детерминированному имени `<rvs.Name>-admin-lock`.
   - Если op уже `Running` → publish `AdminLocked=True/Acquired` → `Continue`.
   - Если `Pending` → `AdminLocked=False/Acquiring` → requeue.
   - Если `Failed` → удаляем op (агент дочистит kernel state) → `AdminLocked=False/RetryingTransient` → requeue.
   - Если op нет:
     - `computeTargetAdminLockHolder`: первый attached diskful primary в `rv.Status.Datamesh.Members`; иначе первый diskful в `rvrs`. Если не найден → `AdminLocked=False/NoHolder` → requeue.
     - `getDRBDR(holder)` + проверка `Spec.State==Up` → иначе `AdminLocked=False/HolderUnready` → requeue.
     - Pre-check `computeAdminLockReadiness` для всех RVR: для каждой реплики DRBDResource должен быть UpToDate (Diskful) или Diskless (TieBreaker), все peer connections в `Connected`+`Established`. Иначе `AdminLocked=False/ClusterNotReady` → requeue. Это снижает риск долгого `wait_for_local_drain` в kernel и timeout'а на acquire.
     - `createAdminLockOp` с `OwnerReference` на RVS → `AdminLocked=False/Acquiring` → requeue.

2. **Release** (вызывается на `Phase=Ready`/`Failed` в `reconcileNormal` и сразу в `reconcileDelete`):
   - Если op нет → `AdminLocked=False/Released` → `Continue`.
   - Иначе → `deleteAdminLockOp` (агент через finalizer вызывает `unlock`) → `AdminLocked=False/Releasing` → requeue до полного удаления op'а.

Идентификатор lock'а — детерминированное имя `<rvs.Name>-admin-lock`, что делает рестарт контроллера идемпотентным. `OwnerReference` гарантирует GC при удалении RVS даже при abnormal termination контроллера.

### Покрытие сценариев из исходных опасностей

| Опасность | Решение в текущей реализации |
|---|---|
| A.1 ssh-обход `drbdadm primary` | Kernel-уровень: `ERR_LOCK_HELD` |
| A.2 race-окно от create до Running | Один op атомарно через twopc; caller дожидается `Phase=Running` перед prepare/sync |
| A.3 корректность caller'а | Детерминированное имя + `OwnerReference` + finalizer на agent-стороне |
| A.4 network split | Twopc требует quorum; partition без quorum не возьмёт lock (а snapshot и не должен идти) |
| A.5 несогласованность с DRBD-state | Lock и admin handler'ы держатся на одних mutex'ах в ядре |
| A.6 шум etcd | Один CR на RVS вместо N |
| B.2 backward compat | `DRBD_FF_ADMIN_LOCK` feature flag в DRBD-handshake; на старых ядрах `lock` возвращает `ErrLockNotSupported`, RVS никогда не дойдёт до acquire |
| B.4 lock owner death | Owner привязан к genl-сокету; close → kernel сам инициирует unlock через twopc |

### Что не делается прямо сейчас

- Поведение при сборке кластера со старыми ядрами без `DRBD_FF_ADMIN_LOCK`: пока RVS будет вечно в `Pending` с reason=`Acquiring` (op в `Failed` → retry → ...). Нужен явный downgrade-путь («lock not supported → snapshot fallback to legacy unsafe path or refuse»). Решается в отдельной итерации после обсуждения, см. RFC [external-read-in-progress-rfc.md](external-read-in-progress-rfc.md).
- Multi-resource lock (несколько RV атомарно для cross-volume snapshot) — пока не нужен, добавится при необходимости как новый CR `DRBDResourcesLock` поверх того же netlink-вызова с массивом ресурсов.
- Аналогичная интеграция в clone flow (`rv-controller`) — будет следующей итерацией; контракт лока для caller'а тот же, что у RVS.

## Сводное сравнение

| Аспект | Подход A (control-plane DRBDOp) | Подход B (cluster-wide kernel + twopc) |
|---|---|---|
| Time to prototype | 2-4 дня календарных | 3-5 недель календарных |
| Time to production-ready | 1.5-2 недели | 1.5-2 месяца |
| Защита от обхода вручную (ssh + drbdadm) | Нет | Да |
| Атомарность взятия лока | Нет (окно от create до Running на всех N) | Да (twopc «всё или ничего») |
| Стек / знакомая область | Go, k8s — наша обычная зона | C, DRBD-kernel — для большинства новая |
| Backward compat внутри кластера | Не зависит от версии DRBD-модуля | Требует одинаковую/совместимую версию модуля на всех узлах |
| Observability | `kubectl get/describe/events`, audit log API-сервера | `drbdsetup status` / debugfs; `kubectl` — только если синкаем в Status |
| Partition behavior | Симметричное окно (split = нет ни лока, ни admin op'ов) | Требует явного quorum-policy дизайна |
| Blast radius bug'ов | Pod рестартует, etcd-state остаётся | Возможен kernel-hang/panic; стопор кластера на координации |
| Эволюционируемость в гибрид | Контракт наружу можно сохранить и подменить реализацию на kernel | Уже самый низкий уровень — некуда «вглубь» |
| Liveness / cleanup orphan-лока | OwnerReference + k8s-GC | Привязка к открытому netlink-сокету или TTL-renew |
| Multi-resource lock (несколько RV атомарно) | Естественно через композицию controller'ов | Требует нового механизма поверх twopc |

## Возможный гибрид

Подход A не закрывает дверь к Подходу B. Контракт наружу один и тот же — «caller просит лок и приступает к снапшоту/клону, когда лок взят». Реализация под капотом может эволюционировать с A на B без изменений в caller'ах.

Поэтапная лестница (как опция):

- **Phase 0: Подход A в нашем агенте.** 2-4 дня. Закрывает 95% сценариев — потому что в нашем кластере единственный admin-caller это сам агент. Если оператор полез руками — это уже out-of-band, и снапшот в это время делать не надо.
- **Phase 1: per-replica kernel lock без twopc.** Агент в `reconcileLockAdmin` дополнительно дёргает `drbdsetup lock` на локальном ресурсе. Защита от обхода появляется. Кластер-вайд гарантий ещё нет, но каждый узел теперь жёстко защищён. ~2-3 недели сверх Phase 0.
- **Phase 2: cluster-wide kernel lock через twopc.** Атомарность взятия и единственный источник правды. ~3-5 недель сверх Phase 1.

Каждая стадия независимо ценна и может стать конечной, если дальнейшая инвестиция не оправдана. На любой стадии остановка не «выкидывает» предыдущую работу.

## Открытые вопросы

1. ~~Точный whitelist snapshot/IO-операций vs admin-операций.~~ Зафиксирован в kernel-патче: `SuspendIO`, `ResumeIO`, `NewCurrentUUID`, `TrackBitmap`, `FlushBitmap`, `lock`/`unlock`/`force-unlock`. См. `3p-drbd-old/drbd/drbd_nl.c`.
2. ~~Гранулярность лока: per-DRBDResource vs per-RV.~~ Per-RV (один `LockAdmin` op на RVS), реализовано через twopc.
3. **ForceDetach / Emergency.** На сегодня bypass нет — все non-whitelist admin-команды отвергнутся `ERR_LOCK_HELD`. Аварийный escape: `force-unlock` через CR `DRBDOp{Type=ForceUnlockAdmin}` или `drbdsetup force-unlock` руками — оба пути снимают lock через twopc.
4. **RV уже multi-primary в момент `lock`.** В текущем дизайне `lock` не требует определённой роли — он закроет gate на новые admin-команды независимо от текущей роли. Принудительная демоция остаётся ответственностью caller'а.
5. **Backward compat в смешанном кластере.** Как именно сообщать пользователю, что lock невозможен из-за старого ядра на части нод — открыто; пока RVS зависает в `Acquiring` (видимо в condition). Нужен либо явный refuse + Event, либо downgrade-path на legacy unsafe snapshot — обсуждается в [external-read-in-progress-rfc.md](external-read-in-progress-rfc.md).
6. **Clone flow.** Перенос той же интеграции из rvs-controller в rv-controller — отдельная задача; контракт уже стабилизирован.
