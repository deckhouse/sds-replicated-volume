# Базовые горутины
## Каждая горутина пишет в лог:
- при начале действия:
  - имя rv
    - название действия и параметры
  - при окончании действия
    - имя rv
    - название действия и параметры
    - результат
    - сколько заняло времени
  - если она следит, то при смене состояния
    - имя rv
    - ожидаемое состояние
    - наблюдаемое состояние

## Горутины
### volume-checker(rv)
Собирает статистику по переходам состояния rv ориентируясь на condition.
  - следит (Watch вместо Get каждые N секунд), что с rv все ок.
    - condition остается RV.ioReady==True
    - condition остается RV.Quorum==True
  - при переключении состояния - писать в лог с Reason и Message если Condition status меняется. Записать в структуру rvName, кол-во переходов для каждого из condition, в начале condition должны быть в true. Написать  в лог condition rvr == false.
    Таким образом четное кол-во переходов указывает на то, что rv поддерживает нужное состояние несмотря на попытки ее развалить, а нечетное, что попытки удались. В идеале нужно иметь счетчики переходов по нулям.
  - когда получает сигнал окончания — выходит

### volume-attacher (rv, period_min, period_max)
Эмулирует работу CSI, публикуя RV на разных нодах через ресурсы **RVA** (`ReplicatedVolumeAttachment`).
  - в цикле:
    - ждет рандом
    - случайным образом выбирает одну ноду(wantedNodeName) с label sds-replicated-volume.
    - в зависимости от количества активных **RVA** (т.е. желаемых прикреплений):
      - 0: 
        - rand(100) > 10 - обычный цикл (добавим одну и уберем одну) (0 нод на выходе)
        - rand(100) < 10 - Attach цикл (только добавить 1 ноду) (1 нод на выходе)
      - 1 : 
        - wantedNodeName не находится среди RVA - тогда цикл эмуляции миграции (создаём новую RVA, удаляем старую RVA, затем удаляем новую) (0 нод на выходе)
        - wantedNodeName уже находится среди RVA - тогда только detach цикл (удалить RVA) (0 нод на выходе)
      - 2: - кейс когда контроллер упал и поднялся
        - wantedNodeName находится или не находится среди RVA - делаем Detach цикл, удаляем случайную RVA (1 на выходе).
                 
      Таким образом у нас большая часть будет с 0 нод(вне цикла работы volume-attacher), а часть с 1 нодой для эмуляции миграции.
      Итого:
      из 0 нод с шаном 5% мы делаем 1 ноду(без этого у нас всегда будет оставаться 0 и мы спустя какое-то время после старта никогда не получим 2), а обычно не делаем(оставлем 0 на выходе)
      из 1 ноды мы делаем 0, но с разным подходом: либо сразу либо с эмуляцией миграции(временно делаем 2, затем 0)
      из 2 нод мы делаем 1.
      

    - **Обычный цикл** (добавим одну и уберем одну):
      - делает действие паблиш: **создаёт RVA** для выбранной ноды (не затрагивая другие RVA).
        - дожидается успеха: `rva.status.conditions[type=Ready].status=True` (агрегат: `Attached=True` и `ReplicaIOReady=True`) и/или `rv.status.actuallyAttachedTo` содержит выбранную ноду.
      - ждет рандом
      - делает действие анпаблиш **выбранной ноды**: удаляет соответствующую RVA (если она существует)
        - дожидается успеха: `rv.status.actuallyAttachedTo` не содержит выбранную ноду (и/или RVA удалена).
        - пишет в лог о любых действиях или бездействиях (когда ноды 2)
    - **Detach цикл** (убрать одну ноду):
      - действие анпаблиш **выбранной ноды**: удаляет RVA (если она существует)
        - дожидается успеха: `rv.status.actuallyAttachedTo` не содержит выбранную ноду
        - пишет в лог о любых действиях или бездействиях (когда ноды 2)
    - **Attach цикл** (только добавить 1 ноду):
      - делает действие паблиш: создаёт RVA для выбранной ноды
        - дожидается успеха: RVA `Ready=True` и/или `rv.status.actuallyAttachedTo` содержит выбранную ноду
        - пишет в лог
    - **Цикл эмуляции миграции** (создаём новую RVA, удаляем старую RVA, затем удаляем новую)
      - делает действие паблиш: создаёт RVA для выбранной новой ноды
        - дожидается успеха: `rv.status.actuallyAttachedTo` содержит выбранную новую ноду (и при необходимости обе, если в итоге должно быть 2)
      - действие анпаблиш **старой ноды**: удаляет RVA старой ноды
        - дожидается успеха: `rv.status.actuallyAttachedTo` не содержит старую ноду
        - пишет в лог о любых действиях или бездействиях (когда ноды 2)
      - ждет рандом
      - действие анпаблиш **выбранной новой ноды**: удаляет RVA выбранной новой ноды
        - дожидается успеха: `rv.status.actuallyAttachedTo` не содержит выбранную новую ноду
        - пишет в лог о любых действиях или бездействиях (когда ноды 2)

  - когда получает сигнал окончания
    - делает действие анпаблиш
      - удаляет все RVA для данного RV
      - дожидается успеха 
    - выходит

### volume-resizer(rv, period_min, period_max, step_min, step_max) - ОТЛОЖЕНО!
Меняет размеры rv.
TODO: не увеличивать размер > maxRvSize
  - в цикле 
    - ждет рандом
    - делает действие ресайза
      - увеличивает размер в rv на случайный размер в диапазоне
      - дожидается успеха
  - когда получает сигнал окончания
    - выходит

### volume-replica-destroyer (rv, period_min, period_max)
Удаляет случайные rvr у rv.
  - в цикле пока не выйдем, с случайным интервалом из (period_min+max)
    - ждет рандом(в интервале выше)
    - случайным образом выбирает rvr из тех которые у нас в данном rv.
    - выполняет действие удаления:
      - вызывает delete на rvr
      - НЕ дожидается успеха
      - пишет в лог , который уже структурирован, действие
  - когда получает сигнал окончания
    - выходит

### volume-replica-creator (rv, period_min, period_max)
Создает случайные rvr у rv.
  - в цикле пока не выйдем, с случайным интервалом из (period_min+max)
    - ждет рандом (в интервале выше)
    - случайным образом выбирает тип rvr:
      - Access или TieBreaker
      - Diskful пока не создаем (у нас нет удалятора лишних diskful пока)
    - выполняет действие создания rvr c выбранным типом.
      - создает rvr
      - НЕ дожидается успеха
      - пишет в лог, который уже структурирован, тип и действие
  - когда получает сигнал окончания
    - выходит

### volume-main (rv, sc, lifetime_period)
  - рандомом выбирает, сколько нод сразу в паблиш (это первоначальное состояние кластера при запуске megatest, далее поддерживать такое не надо)
    - 0 — 30%
    - 1 — 60%
    - 2 — 10%
  - рандомом выбирает, то количество нод, которое получили на предыдущем шаге
  - выполняет действие создать rv
    - создает rv
    - запускает:
      - volume-attacher(rv, 30, 60) - подумать над интервалами
      - volume-attacher(rv, 100, 200) - РЕШИЛИ НЕ ДЕЛАТЬ!
      - volume-resizer(rv, 50, 50, 4kb, 64kb) - ОТЛОЖЕНО! - контроллер ресайза может увеличить rv больше чем запрошено, если это требуется на более низком уровне, поэтому проверка должна это учитывать. Но нужно уточнить порог срабатывания  sds-node-configurator - он может не увеличивать на малые значения. 
      - volume-replica-destroyer (rv, 30, 300)
      - volume-replica-creator (rv, 30, 300)
    - дожидается, что станет ready
  - запускает
    - volume-checker(rv)
  - когда ей посылают сигнал окончания или истекает lifetime_period
    - останавливает:
      - volume-checker
      - volume-attacher’ы
    - выполняет действие удаление rv
      - дожидается успеха
    - останавливает
      - volume-resizer
      - volume-replica-destroyer
      - volume-replica-creator
    - выходит

### pod-destroyer (ns, label_selector, pod_min, pod_max, period_min, period_max)
Удаляет поды control-plane по label_selector
  - в цикле:
    - ждет рандом rand(period_min, period_max)
    - выбирает поды с заданным label_selector, перемешивает список (на статус не смотрит)
    - выбирает случайное число из (rand(pod_min, pod_max))
    - делает delete выбранного числа pod'ов с начала списка
    - не дожидается удаления
  - когда ей посылают сигнал окончания
    - выходит

### multivolume(list sc, max_vol, step_min, step_max, step_period_min, step_period_max, vol_period_min, vol_period_max)
Оркестратор горутин (он же main). 
  - запускает:
    - pod-destroyer(agent, 1, 2, 30, 60)
    - pod-destroyer(controller, 1, 3, 30, 60)
    - pod-destroyer(kube-apiserver, 1, 3, 120, 240) - ПОКА НЕ ДЕЛАЕМ (т.е. kube-apiserver это статичный под)!
  - в цикле
    - если количество запущенных volume_main < max_vol
      - выбирает случайным образом количество для запуска (step_min, step_max), может превышать max_vol
      - в цикле для каждого N
        - выбирает случайный scName
        - выбирает случайный vol_period
        - генерирует случайное имя rvName
        - запускает volume-main(rvName, scName, vol_period)
    - ждет рандом(step_period_min, step_period_max)
  - когда ей посылают сигнал окончания
    - останавливает всё запущенное
    - выходит

# Chaos Engineering горутины
Для работы chaos-горутин требуется чтобы кластер для тестирования был вложенным кластером развернутым в DVP и следовательно появляются требования:
- второй kubeconfig для родительского кластера (--parent-kubeconfig)
- namespace где находятся VM тестируемого кластера (--vm-namespace)

При старте:
- очищает stale ресурсы от предыдущих запусков (CiliumNetworkPolicy, VirtualMachineOperation и т.д.)

При завершении:
- удаляет все созданные CiliumNetworkPolicy
- удаляет все созданные VirtualMachineOperation

## Горутины
### chaos-network-blocker (period_min, period_max, incident_min, incident_max)
Блокирует всю сеть между случайными парами нод вложенного кластера через **CiliumNetworkPolicy** в родительском кластере,
при этом оставляя трафик между мастерами родительского и вложенного кластеров.
  - в цикле:!!!!!!
    - ждет рандом(period_min, period_max)
    - получает список VM из parent-кластера
    - случайным образом выбирает пару нод (nodeA, nodeB)
    - создаёт **CiliumClusterwideNetworkPolicy** с:
      - nodeSelector на nodeA
      - ingressDeny/egressDeny для IP nodeB (все порты)
    - Note: блокировка односторонняя (nodeA → nodeB). Этого достаточно для DRBD тестов.
      Для полной изоляции (bidirectional) используйте chaos-network-partitioner.
    - ждет рандом(incident_min, incident_max)
    - удаляет созданную policy
    - пишет в лог действия с именами нод
  - когда получает сигнал окончания
    - удаляет активную policy если есть
    - выходит

### chaos-drbd-blocker (period_min, period_max, incident_min, incident_max)
Блокирует DRBD порты между случайными парами нод вложенного кластера через **CiliumNetworkPolicy** в родительском кластере,
при этом оставляя трафик между мастерами родительского и вложенного кластеров.
  - в цикле: !!!!!!!
    - ждет рандом(period_min, period_max)
    - получает список VM из parent-кластера (namespace --vm-namespace)
    - случайным образом выбирает пару нод (nodeA, nodeB)
    - **собирает актуальные DRBD порты** из RVR CRD (`status.drbd.config.address.port`)
      - быстро (<100ms), не требует privileged Jobs
      - **если порты не найдены - инцидент пропускается** (ожидает создания RV life-simulation)
    - создаёт **CiliumClusterwideNetworkPolicy** с:
      - nodeSelector на nodeA
      - ingressDeny/egressDeny для IP nodeB на обнаруженных DRBD портах
    - ждет рандом(incident_min, incident_max) - длительность инцидента
    - удаляет созданную policy
    - пишет в лог действия с именами нод и портами
  - когда получает сигнал окончания
    - удаляет активную policy если есть
    - выходит

### chaos-network-partitioner (period_min, period_max, incident_min, incident_max, group_size)
Создаёт split-brain: разделяет ноды вложенного кластера на группы и блокирует всю сеть между ними через **CiliumNetworkPolicy** в родительском кластере,
при этом оставляя трафик между мастерами родительского и вложенного кластеров.
  - в цикле: !!!!!!!!
    - ждет рандом(period_min, period_max)
    - получает список VM из parent-кластера
    - разделяет ноды на две группы:
      - если group_size > 0: группа A = group_size нод, группа B = остальные
      - если group_size = 0: делит пополам
    - для каждой пары (nodeA из группы A, nodeB из группы B):
      - создаёт **CiliumClusterwideNetworkPolicy** с блокировкой всей сети
    - ждет рандом(incident_min, incident_max)
    - удаляет все созданные policies
    - пишет в лог состав групп и количество policies
  - когда получает сигнал окончания
    - удаляет все активные policies
    - выходит

### chaos-network-degrader (period_min, period_max, incident_min, incident_max, delay_ms, loss_percent, rate_kbit)
Деградирует сеть (latency + packet loss + bandwidth limit) между парами нод вложенного кластера.
Решили отказаться от tc (сложно подружить с cilium) и использовать во вложенном кластере:
  - iptables для потери пакетов
  - iperf3 для увеличения latency + нагрузка на сеть
  - bandwidth limit не делаем, т.к. вероятность, что сеть будет слишком узкая крайне мала. Скорее сеть будет нагружена, что покрывается iperf3.
  - в цикле:!!!!
    - ждет рандом(period_min, period_max)
    - получает список VM из parent-кластера
    - случайным образом выбирает пару нод (nodeA, nodeB)
    - выбирает параметры деградации:
      - delay: рандом(delay_ms_min, delay_ms_max) мс
      - jitter: delay/4 мс
      - loss: рандом(loss_percent_min, loss_percent_max) %
      - rate: рандом(rate_mbit_min, rate_mbit_max) mbit/s (0 = без ограничения)
    - создаёт privileged **Job** на nodeA с hostNetwork:true:
      - определяет интерфейс для достижения nodeB через `ip route get`
      - применяет tc qdisc с уникальными handles (31337/31338) для избежания конфликтов
      - добавляет netem правило с delay/loss/rate для трафика к nodeB
      - **auto-cleanup**: если megatest не очистит rules в течение incident_duration + 120s, Job сам удалит правила
    - Note: деградация односторонняя (nodeA → nodeB), этого достаточно для DRBD тестов
    - ждет рандом(incident_min, incident_max)
    - создаёт cleanup **Job** на nodeA:
      - удаляет только tc правила с нашими handles (structure-based detection)
    - пишет в лог действия с параметрами
  - когда получает сигнал окончания
    - создаёт cleanup Jobs для всех затронутых нод
    - выходит

### chaos-vm-reboter (period_min, period_max)
Выполняет hard reboot случайных VM через **VirtualMachineOperation** в родительском кластере.
  - в цикле:
    - ждет рандом(period_min, period_max)
    - получает список VM из parent-кластера
    - случайным образом выбирает одну VM
    - создаёт **VirtualMachineOperation** с:
      - type: Restart
      - force: true (hard reboot)
    - НЕ дожидается завершения операции
    - пишет в лог имя VM
  - когда получает сигнал окончания
    - выходит

## CLI флаги для chaos
```
--parent-kubeconfig              # путь к kubeconfig parent-кластера DVP (обязателен для chaos)
--vm-namespace                   # namespace с VM в parent-кластере (обязателен для chaos)

--enable-chaos-drbd-block        # включить chaos-drbd-blocker
--enable-chaos-network-block     # включить chaos-network-blocker
--enable-chaos-network-degrade   # включить chaos-network-degrader
--enable-chaos-vm-reboot         # включить chaos-vm-reboter
--enable-chaos-network-partition # включить chaos-network-partitioner

--chaos-period-min               # мин. интервал между инцидентами (default: 60s)
--chaos-period-max               # макс. интервал между инцидентами (default: 300s)
--chaos-incident-min             # мин. длительность инцидента (default: 10s)
--chaos-incident-max             # макс. длительность инцидента (default: 60s)

--chaos-delay-ms-min             # мин. задержка сети в мс (default: 30)
--chaos-delay-ms-max             # макс. задержка сети в мс (default: 60)
--chaos-loss-percent-min         # мин. потеря пакетов % (default: 1.0)
--chaos-loss-percent-max         # макс. потеря пакетов % (default: 10.0)
--chaos-rate-mbit-min            # мин. ограничение bandwidth в mbit/s (default: 5)
--chaos-rate-mbit-max            # макс. ограничение bandwidth в mbit/s (default: 50)
--chaos-partition-group-size     # размер группы для partition (default: 0 = пополам)
```
