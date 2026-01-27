# CLI флаги
```
# Общие
--log-level      # уровень логирования (allowed values: debug, info, warn, error; default: "info")

# Базовые горутины
--storage-classes                    # список storage class через запятую (обязателен)
--kubeconfig                         # путь к kubeconfig файлу (default: "")

--max-volumes                        # максимальное количество одновременных ReplicatedVolumes (default: 10)
--volume-step-min                    # мин. количество ReplicatedVolumes для создания за шаг (default: 1)
--volume-step-max                    # макс. количество ReplicatedVolumes для создания за шаг (default: 3)
--step-period-min                    # мин. интервал между шагами создания ReplicatedVolumes (default: 10s)
--step-period-max                    # макс. интервал между шагами создания ReplicatedVolumes (default: 30s)
--volume-period-min                  # мин. время жизни ReplicatedVolume (default: 1m0s)
--volume-period-max                  # макс. время жизни ReplicatedVolume (default: 5m0s)

--enable-pod-destroyer               # включить pod-destroyer горутины (default: false)
--enable-volume-resizer              # включить volume-resizer горутину (default: false)
--enable-volume-replica-destroyer    # включить volume-replica-destroyer горутину (default: false)
--enable-volume-replica-creator      # включить volume-replica-creator горутину (default: false)

# Chaos
--parent-kubeconfig              # путь к kubeconfig родительского кластера DVP (обязателен для chaos, default: "")
--vm-namespace                   # namespace с VM в родительском кластере (обязателен для chaos, default: "")

--enable-chaos-network-block     # включить chaos-network-blocker (поддерживает blocking-everything, blocking-drbd, split-brain) (default: false)
--enable-chaos-network-degrade   # включить chaos-network-degrader (поддерживает losses, latency) (default: false)
--enable-chaos-vm-reboot         # включить chaos-vm-reboter (default: false)

--chaos-period-min               # мин. интервал между инцидентами (default: 1m0s)
--chaos-period-max               # макс. интервал между инцидентами (default: 5m0s)
--chaos-incident-min             # мин. длительность инцидента (default: 10s)
--chaos-incident-max             # макс. длительность инцидента (default: 1m0s)

--chaos-loss-percent             # потеря пакетов %; принимает значение от 0.0 до 1.0 (0.01 = 1%, 0.10 = 10%; default: 0.01)
--chaos-partition-group-size     # размер группы для partition в chaos-network-blocker (default: 0 = пополам)
```

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
        - дожидается успеха: `rva.status.conditions[type=Ready].status=True` (агрегат: `Attached=True` и `ReplicaReady=True`) и/или `rv.status.actuallyAttachedTo` содержит выбранную ноду.
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
## multivolume(list sc, max_vol, step_min, step_max, step_period_min, step_period_max, vol_period_min, vol_period_max)
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
- очищает оставшиеся ресурсы от предыдущих запусков
  - удаляет все существующие `CiliumNetworkPolicy` в родительском кластере
  - удаляет все существующие `Job` во вложенном кластере

## Горутины
### chaos-network-blocker (period_min, period_max, incident_min, incident_max, group_size)
Блокирует сеть между VM вложенного кластера через `CiliumNetworkPolicy` в родительском кластере,
при этом оставляя трафик между родительским кластером и вложенным.
Для выделения выбранных VM в `CiliumNetworkPolicy` используется лейбл `vm.kubevirt.internal.virtualization.deckhouse.io/name: vmName` у пода, где `vmName` это имя VM. 
Перед созданием `CiliumNetworkPolicy` нужно проверять, существование политики с таким именем и если она есть, то удалять.
  - в цикле:
    - ждет рандом(period_min, period_max)
    - получает список VM из родительского кластера и рандомно его перемешивает
    - rand(100) <= 30 (вариант инцидента **blocking-everything**)
      - берет первые две VM
      - создаёт `CiliumNetworkPolicy` в родительском кластере для блокировки всего tcp трафика между ними
    - rand(100) > 30 и <= 60 (вариант инцидента **blocking-drbd**)
      - берет первую VM
      - получает все RV (или что-то другое) для этой VM и выбирает случайным образом пару DRBD портов
      - создаёт `CiliumNetworkPolicy` в родительском кластере для блокировки выбранных DRBD портов
    - rand(100) > 60 (вариант инцидента **split-brain**)
        - разделяет VM на две группы:
          - если group_size > 0: группа-A = group_size нод, группа-B = остальные
          - если group_size = 0: делит пополам (если нечетное число VM, то группа-А = большая часть, группа-В = меньшая часть)
        - создаёт `CiliumNetworkPolicy` в родительском кластере для блокировки всего tcp трафика между группами нод
    - пишет в лог вариант инцидента +
      - для **blocking-everything**: имена VM
      - для **blocking-drbd**: имя VM и DRBD порты
      - для **split-brain**: имена VM в каждой из групп
    - ждет рандом(incident_min, incident_max) - длительность инцидента
    - удаляет созданные policy
  - когда получает сигнал окончания
    - удаляет активную policy если есть
    - выходит

### chaos-network-degrader (period_min, period_max, incident_min, incident_max, loss_percent)
Деградирует сеть (latency + packet loss) между парой VM.
Решили не использовать tc т.к. сложно подружить с cilium.
Все манипуляции производятся во вложенном кластере в ns default.
Перед созданием `Job` нужно проверять, существование джобы с таким именем и если она есть, то удалять.
  - в цикле:
    - ждет рандом(period_min, period_max)
    - получает список VM из родительского кластера и рандомно его перемешивает
    - берет первые две VM (nodeA, nodeB)
    - выбирает incident_lifetime = рандом(incident_min, incident_max)
    - rand(100) <= 50 (вариант инцидента **losses**)
      - создаёт две privileged `Job` для каждой из VM с `hostNetwork:true` и командами:
        - первая джоба:
          ```sh
          set -e
          iptables -A INPUT -s 10.211.1.47 -m statistic --mode random --probability 0.01 -j DROP -m comment --comment "this-name-of-job"
          echo "iptables rule added"
          ```
        - вторая джоба:
          ```sh
          set -e
          sleep $incident_lifetime
          COMMENT="this-name-of-job"
          while iptables -L INPUT --line-numbers | grep -q "$COMMENT"; do
            iptables -D INPUT $(iptables -L INPUT --line-numbers | grep -F "$COMMENT" | head -n1 | awk '{print $1}')
          done
          ```
        где: 10.211.1.47 - это ip противоположной VM (для nodeA это ip от nodeB и наоборот); 0.01 - это loss_percent
    - rand(100) > 50 (вариант инцидента **latency**)
      - создаёт privileged `Job` на каждой из VM с `hostNetwork:true` и командами:
        ```sh
        set -e
        timeout $incident_lifetimes sh -c '
          iperf3 -s -D || true
          while true;do
            iperf3 -c 10.211.1.47 -t 60 -i 30 || true
            sleep 0.5
         done
        ' || true
        ```
        где 10.211.1.47 - это ip противоположной VM
    - пишет в лог вариант инцидента + имена VM
    - ждет incident_lifetime
  - когда получает сигнал окончания
    - удаляет активные джобы
    - выходит

### chaos-vm-reboter (period_min, period_max)
Выполняет hard reboot случайных VM через `VirtualMachineOperation` в родительском кластере.
  - в цикле:
    - ждет рандом(period_min, period_max), если период получается меньше 5 минут, то ждем рандом от 5 до 6 минут
    - получает список VM из родительского кластера и рандомно его перемешивает
    - для первой VM получает `VirtualMachineOperation`
      - \> 0
        - во всех `VirtualMachineOperation` считает status.phase = Failed | Completed
          - количество `VirtualMachineOperation` != посчитанному количеству status.phase
            - идет на следующую итерацию цикла (то есть ничего не делаем для этой VM), при этом записав в лог, что для выбранной VM есть незавершенные `VirtualMachineOperation`
    - для первой VM из списка создаёт `VirtualMachineOperation` с:
      - spec.type: Restart
      - spec.force: true
    - НЕ дожидается завершения операции
    - пишет в лог имя VM
  - когда получает сигнал окончания
    - выходит

## Примеры
```yaml
apiVersion: virtualization.deckhouse.io/v1alpha2
kind: VirtualMachineOperation
metadata:
  name: megatest-chaos-restart-zsz9c
  namespace: e2e-sds-test-cluster
spec:
  type: Restart
  force: true
  virtualMachineName: worker-11
```

```yaml
---
# запретить только один порт между двумя подами
apiVersion: cilium.io/v2
kind: CiliumNetworkPolicy
metadata:
  name: deny-5201-only-between-worker-01-and-worker-10
  namespace: e2e-sds-test-cluster
spec:
  endpointSelector:
    matchExpressions:
      - key: vm.kubevirt.internal.virtualization.deckhouse.io/name
        operator: In
        values: ["worker-01", "worker-10"]

  ingress:
    - fromEntities:
        - host
        - remote-node
    - fromEndpoints:
        - matchLabels:
            io.kubernetes.pod.namespace: e2e-sds-test-cluster
  egress:
    - toEntities:
        - host
        - remote-node
    - toEndpoints:
        - matchLabels:
            io.kubernetes.pod.namespace: e2e-sds-test-cluster

  ingressDeny:
    - fromEndpoints:
        - matchExpressions:
            - key: vm.kubevirt.internal.virtualization.deckhouse.io/name
              operator: In
              values: ["worker-01", "worker-10"]
      toPorts:
        - ports:
            - port: "5201"
              protocol: TCP

  egressDeny:
    - toEndpoints:
        - matchExpressions:
            - key: vm.kubevirt.internal.virtualization.deckhouse.io/name
              operator: In
              values: ["worker-01", "worker-10"]
      toPorts:
        - ports:
            - port: "5201"
              protocol: TCP

---
# запретить весь tcp между двумя подами
apiVersion: cilium.io/v2
kind: CiliumNetworkPolicy
metadata:
  name: deny-all-tcp-between-worker-01-and-worker-10
  namespace: e2e-sds-test-cluster
spec:
  endpointSelector:
    matchExpressions:
      - key: vm.kubevirt.internal.virtualization.deckhouse.io/name
        operator: In
        values: ["worker-01", "worker-10"]

  ingress:
    - fromEndpoints:
        - matchLabels:
            io.kubernetes.pod.namespace: e2e-sds-test-cluster
  egress:
    - toEndpoints:
        - matchLabels:
            io.kubernetes.pod.namespace: e2e-sds-test-cluster

  ingressDeny:
    - fromEndpoints:
        - matchExpressions:
            - key: vm.kubevirt.internal.virtualization.deckhouse.io/name
              operator: In
              values: ["worker-01", "worker-10"]
      toPorts:
        - ports:
            - port: "1"
              endPort: 65535
              protocol: TCP

  egressDeny:
    - toEndpoints:
        - matchExpressions:
            - key: vm.kubevirt.internal.virtualization.deckhouse.io/name
              operator: In
              values: ["worker-01", "worker-10"]
      toPorts:
        - ports:
            - port: "1"
              endPort: 65535
              protocol: TCP
```

```yml
apiVersion: batch/v1
kind: Job
metadata:
  name: worker-01-del-iptables
  namespace: default
spec:
  ttlSecondsAfterFinished: 600          # incident_lifitime + 4sec
  backoffLimit: 0                       # не перезапускать при ошибке
  activeDeadlineSeconds: 180            # incident_lifetime + 2sec
  template:
    spec:
      restartPolicy: Never
      hostNetwork: true
      nodeName: worker-01
      terminationGracePeriodSeconds: 1
      containers:
      - name: net-tools
        image: krpsh/iperf3:0.1.0
        securityContext:
          privileged: true
        command: ["/bin/sh", "-c"]
        args:
        - |
          set -e
          sleep 30
          COMMENT="worker-01-add-iptables"
          while iptables -L INPUT --line-numbers | grep -q "$COMMENT"; do
            NUMBER=$(iptables -L INPUT --line-numbers | grep -F "$COMMENT" | head -n1 | awk '{print $1}')
            echo "delete rule number $NUMBER"
            iptables -D INPUT $NUMBER
          done
```


```sh
# Дропать ~10% всех входящих пакетов
iptables -A INPUT -m statistic --mode random --probability 0.10 -j DROP

# Дропать ~5% только TCP-пакетов
iptables -A INPUT -p tcp -m statistic --mode random --probability 0.05 -j DROP

# Дропать ~3% исходящих TCP-пакетов
iptables -A OUTPUT -p tcp -m statistic --mode random --probability 0.03 -j DROP

# Удалить добавленное правило
iptables -L INPUT --line-numbers
iptables -D INPUT 1
```