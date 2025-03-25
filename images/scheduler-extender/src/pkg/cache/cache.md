# SDS-Replicated-Volume Cache Control

Cache это in-memory хранилище для информации о PVC и LVG кластера. Данные в Cache помещает и актуализирует контроллер.

Визуализация кэша с данными:
```go
Cache{
	Lvgs: sync.Map{
		"namespace-1/lvg-1": LvgCache{
			Lvg:       &snc.LVMVolumeGroup{Name: "lvg-1"},
            thickPVCs: sync.Map{
				"namespace-1/pvc-1": &pvcThickCache{
					pvc:          &v1.PersistentVolumeClaim{Name: "pvc-1"},
                    selectedNode: "node-1",
				},
				"namespace-1/pvc-2": &pvcThickCache{
					pvc:          &v1.PersistentVolumeClaim{Name: "pvc-2"},
                    selectedNode: "node-2",
				},
			},
            thinPools: sync.Map{
				"pool-1": &thinPoolCache{
					pvcs: sync.Map{
                        "namespace-1/pvc-2": &pvcCache{
                            pvc:          &v1.PersistentVolumeClaim{Name: "pvc-2"},
                            selectedNode: "node-2",
                        },
						"namespace-1/pvc-3": &pvcCache{
							pvc:          &v1.PersistentVolumeClaim{Name: "pvc-3"},
                            selectedNode: "node-3",
                        },
						}
                    },
				}
			},
		}
	},
    pvcLVGs: sync.Map{
		"namespace-1/pvc-1": []string{"lvg-1"},
        "namespace-1/pvc-2": []string{"lvg-2"},
        "namespace-1/pvc-3": []string{"lvg-3"},
	},
    nodeLVGs: sync.Map{
		"namespace-1/node-1": []string{"lvg-1"},
        "namespace-1/node-2": []string{"lvg-2"},
        "namespace-1/node-3": []string{"lvg-3"},
	},
    log:             logger.NewLogger(),
    expiredDuration: time.Duration(5) * time.Second,
}
```
## Методы кеша
```go
clearBoundExpiredPVC()
```
Метод выполняет очистку кеша от устаревших PVC в состоянии Bound.

Алгоритм работы:
1. Метод итерирует по всем lvg поля Lvgs
2. Запрашиваются все pvc относящиеся к каждой lvg
3. PVC удаляется из кэша если *одновременно*: 
    - ее статус pvc.Status.Phase == v1.ClaimBound
    - с момента ее создания (pvc.CreationTimestamp.Time) прошло больше времени, чем указано в поле expiredDuration кэша

```go
GetAllPVCForLVG(lvgName string) ([]*v1.PersistentVolumeClaim, error)
```
Метод получает все хранящиеся в кэше pvc, связанных с переданной lvg

Алгоритм работы:
1. Метод проверяет наличие pvc в кэше по переданному имени lvg
2. Затем метод итерирует по всем объектам полей thickPVCs, thinPools. Для каждой существующей lvg счетчик size увеличивается на 1
3. Создается слайс для pvc, с cap == size, в него перед возвратом помещаются указатели на pvc из полей thickPVCs и thinPools
4. Метод возвращает слайс


```go
GetAllLVG() map[string]*snc.LVMVolumeGroup
```
Метод возвращает все хранящиеся LVG в виде мапы map[string]*snc.LVMVolumeGroup


```go
GetLVGThickReservedSpace(lvgName string) (int64, error)
```
Метод возвращает суммарное зарезервированное место всеми PVC ThickLVG.
Значение зарезервированного места возвращает метод pvc.Spec.Resources.Requests.Storage().Value()


```go
GetLVGThinReservedSpace(lvgName string, thinPoolName string) (int64, error)
```
Метод подсчитывает количество (в байтах) зарезервированного места всеми thin PVC выбранной LVG


```go
RemovePVCFromTheCache(pvc *v1.PersistentVolumeClaim)
```
Метод удаляет из кэша все данные о переданной PVC


```go
GetLVGNamesForPVC(pvc *v1.PersistentVolumeClaim) []string
```
Метод возвращает слайс имен LVG для выбранной PVC


```go
GetLVGNamesByNodeName(nodeName string) []string
```
Метод возвращает слайс имен LVG для выбранной ноды


```go
UpdateThickPVC(lvgName string, pvc *v1.PersistentVolumeClaim) error
```
Метод обновляет PVC выбранной LVG


```go
AddThickPVC(lvgName string, pvc *v1.PersistentVolumeClaim) error
```
Метод добавляет переданную PVC к выбранной LVG. Если такой LVG нет, возвращает ошибку. Если в кэше есть такая PVC, ничего не делает.


```go
addNewThickPVC(lvgCh *LvgCache, pvc *v1.PersistentVolumeClaim)
```
Метод добавляет переданную PVC к выбранной LVG.


```go
addLVGToPVC(lvgName, pvcKey string)
```
Метод добавляет имя LVG в мапу c.pvcLVGs


```go
shouldAddPVC(pvc *v1.PersistentVolumeClaim, lvgCh *LvgCache, pvcKey, lvgName, thinPoolName string) (bool, error)
```
Метод проверяет, нужно ли добавлять PVC в кэш.

Алгоритм работы:
1. Если PVC не содержит аннотацию выбранного узла (pvc.Annotations["volume.kubernetes.io/selected-node"] == "") метод вернет true
2. Если указанная аннотация присутствует, то:
	- eсли для данного узла не найден список LVG в кэше (nodeLVGs), вернется false
	- если переданная LVG (lvgName) не входит в найденных список, вернется false
	- если LVG принадлежит узлу, производится дальнейшая проверка:
		- если PVC уже присутствует в кэше для thick PVC (lvgCh.thickPVCs), метод логирует это и возвращает false (PVC уже добавлен, его не следует добавлять повторно)
		- далее проверяется случай для thin PVC:
			- если аргумент thinPoolName передан, то метод пытается найти кэш для соответствующего thin pool в lvgCh.thinPools
			- если кэш для thin pool не найден, метод считает, что нужно добавить PVC (возвращает true)
			- если thin pool найден, то производится проверка, существует ли PVC (по ключу pvcKey) внутри кэша thin pool. Если найден, то метод возвращает false (PVC уже добавлен в thin pool)

Резюмируя: добавление происходит только если PVC соответствует выбранному узлу и группе, и ещё не присутствует в кэше thick или thin PVC


```go
UpdateThinPVC(lvgName, thinPoolName string, pvc *v1.PersistentVolumeClaim) error
```
Метод обновляет объект thin PVC в кэше для указанной LVMVolumeGroup и thin pool


```go
addThinPoolIfNotExists(lvgCh *LvgCache, thinPoolName string) error
```
Метод addThinPoolIfNotExists проверяет наличие thin pool с указанным именем в кэше для данной LVMVolumeGroup.

Алгоритм работы:
1. Если имя thin pool не задано (строка пуста), возвращается ошибка.
2. Если thin pool с таким именем уже существует в кэше, метод ничего не делает и возвращает nil.
3. Если thin pool отсутствует, создаётся новый объект thinPoolCache и сохраняется в кэш с ключом thinPoolName, после чего метод возвращает nil.


```go
addNewThinPVC(lvgCh *LvgCache, pvc *v1.PersistentVolumeClaim, thinPoolName string) error
```
Метод создает новый thinPool если он не существует в кэше.
Добавляет pvc в pool, sызывает метод addLVGToPVC.
Чтобы связать PVC с LVMVolumeGroup по lvgCh.Lvg.Name


```go
PrintTheCacheLog()
```
Метод выводит содержимое кэша в лог.


```go
RemoveSpaceReservationForPVCWithSelectedNode(pvc *v1.PersistentVolumeClaim, deviceType string) error
```
Метод удаляет резервацию места для PVC в кэше для всех LVMVolumeGroups, за исключением той группы, в которой PVC закреплён за выбранным узлом.

Алгоритм работы:
1. Метод получает из кэша список LVG, которые связаны с переданной PVC (из pvcLVGs). Если кэш отсутствует, метод завершается
2. Для каждой LVMVolumeGroup ([]string) из п.1:
	- В зависимости от типа устройства (Thin или Thick):
		- Для Thin:
			- Проходит по thin pool’ам, ищет PVC по ключу.
			- Если PVC найден, проверяет поле selectedNode
				- Если selectedNode пустой, удаляет PVC из thin pool’а.
				– Если не пустой, запоминает эту группу как выбранную (selectedLVGName) и PVC не удаляется.
		- Для Thick:
			- Ищет PVC в кэше thickPVCs.
			- Если найдён, проверяет поле selectedNode и либо удаляет резервацию (при пустом selectedNode), либо запоминает группу как выбранную
3. После прохода по всем группам метод обновляет кэш pvcLVGs: оставляет только выбранную LVMVolumeGroup (если таковая имеется) в списке для данного PVC, а для остальных удаляет привязку.

```go
TryGetLVG(name string) *snc.LVMVolumeGroup
```
Метод пытается получить объект LVMVolumeGroup из кэша по заданному имени
Если кэш содержит запись с таким именем, возвращается LVMVolumeGroup (из LvgCache), иначе метод выводит сообщение в лог и возвращает nil

```go
UpdateLVG(lvg *snc.LVMVolumeGroup) error
```
Метод обновляет информацию о LVG в кэше
Алгоритм работы:
	- Проверяет наличие кэша для переданной LVG (c.Lvgs). Метод возвращает ошибку, если кэш не найден
	- Если кэш найден, записывает в поле lvgCh.Lvg переданное значение LVG
	- Для каждого узла из списка lvg.Status.Nodes метод проверяет, содержится ли имя LVG в поле nodeLVGs
		- Если группа ещё не привязана к узлу, добавляет её в список и сохраняет обратно в nodeLVGs
		- Если привязка уже существует, просто логирует этот факт

```go
AddLVG(lvg *snc.LVMVolumeGroup)
```
Метод AddLVG добавляет новый объект LVMVolumeGroup в кэш
Алгоритм работы:
	- Добавляет новый объект в кэш (c.Lvgs) помощью syncMap.LoadOrStore(). Если группа уже там (loaded == true), выводится отладочное сообщение, и метод завершается
	- Если группа добавляется впервые, для каждого узла из lvg.Status.Nodes:
		- Проверяется, существует ли уже список групп для этого узла в nodeLVGs
		- Если списка нет, создаётся новый с вместимостью, рассчитанной на определённое количество групп
		- Добавляется имя группы в список для узла, после чего обновлённый список сохраняется в nodeLVGs
		- Выполняется логирование добавления группы к конкретному узлу

```go
DeleteLVG(lvgName string)
```
Метод удаляет информацию об LVG из кэша (c.Lvgs, c.nodeLVGs, c.pvcLVGs)
