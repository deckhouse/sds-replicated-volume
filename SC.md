# Управление StorageClass

StorageClasses в данном модуле управляются через ресурс DRBDStorageClass. Вручную StorageClasses создаваться не должны.


При миграции с модуля Linstor необходимо создать ресурсы DRBDStorageClass в соответствии с таблицей.
Обратите внимание, что в старых storage class нужно смотреть опцию из секции parameter, а в новом классе нужно использовать опцию самого StorageClass  

| параметр StorageClass                     | DRBDStorageClass      | Параметр по умолчанию | Примечания                                                     |
|-------------------------------------------|-----------------------|-|----------------------------------------------------------------|
| linstor.csi.linbit.com/placementCount: "1" | replication: "None"   | | Будет создаваться одна реплика тома с данными                  |
| linstor.csi.linbit.com/placementCount: "2" | replication: "Availability" | | Будет создаваться две реплики тома с данными.                  |
| linstor.csi.linbit.com/placementCount: "3" | replication: "ConsistencyAndAvailability" | да | Будет создаваться три реплики тома с данными                   |
| linstor.csi.linbit.com/storagePool: "name" | storagePool: "name"   | | Название используемого storage pool для хранения               |
| linstor.csi.linbit.com/allowRemoteVolumeAccess: "false" | volumeAccess: "Local" | | Запрещен удаленный доступ Pod к томам с данными (только локальный доступ к диску в пределах Node) |

Кроме них, можно использовать параметры:

- retentionPolicy (Delete, Retain) - соответствует параметру retentionPolicy у старого StorageClass
- zones - перечисление зон, которые нужно использовать для размещения ресурсов (прямое указание названия зон в облаке). Обратите внимание, что удаленный доступ Pod к тому с данными возможен только в пределах одной зоны!
- volumeAccess может принимать значения "Local" (доступ строго в пределах Node), "EventuallyLocal" (реплика данных будет синхронизироваться на Node с запущенным Pod спустя некоторое время после запуска), "PreferablyLocal", "Any" (На данный момент равноценны, удаленный доступ Pod к тому с данными разрешен)

Пример DRBDStorageClass только с использованием локальных томов и высокой степенью резервирования

```
apiVersion: storage.deckhouse.io/v1alpha1
kind: DRBDStorageClass
metadata:
  annotations:
  name: haclass
spec:
  replication: ConsistencyAndAvailability
  storagePool: storagePoolName
  volumeAccess: Local
  retentionPolicy: Delete
  zones:
  - zone-a
  - zone-b
  - zone-c
```

Пример DRBDStorageClass с возможностью использования удаленных реплик и без резервирования (например, для тестовых окружений)

```
apiVersion: storage.deckhouse.io/v1alpha1
kind: DRBDStorageClass
metadata:
  annotations:
  name: testclass
spec:
  replication: None
  storagePool: storagePoolName
  volumeAccess: Any
  retentionPolicy: Delete
```
