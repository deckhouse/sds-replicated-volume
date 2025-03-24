# SDS-Replicated-Volume Cache Control

Cache это in-memory хранилище для информации о PVC и LVG кластера. Данные в Cache помещает и актуализирует контроллер.

Визуализация кэша с данными:
```go
Cache{
	Lvgs: sync.Map{
		"lvg-1": LvgCache{
			Lvg:       &snc.LVMVolumeGroup{Name: "lvg-1"},
            thickPVCs: sync.Map{
				"pvc-1": &pvcThickCache{
					pvc:          &v1.PersistentVolumeClaim{Name: "pvc-1"},
                    selectedNode: "node-1",
				},
				"pvc-2": &pvcThickCache{
					pvc:          &v1.PersistentVolumeClaim{Name: "pvc-2"},
                    selectedNode: "node-2",
				},
			},
            thinPools: sync.Map{
				"pool-1": &thinPoolCache{
					pvcs: sync.Map{
                        "pvc-2": &pvcCache{
                            pvc:          &v1.PersistentVolumeClaim{Name: "pvc-2"},
                            selectedNode: "node-2",
                        },
						"pvc-3": &pvcCache{
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
		"pvc-1": []string{"lvg-1"},
        "pvc-2": []string{"lvg-2"},
        "pvc-3": []string{"lvg-3"},
	},
    nodeLVGs: sync.Map{
		"node-1": []string{"lvg-1"},
        "node-2": []string{"lvg-2"},
        "node-3": []string{"lvg-3"},
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



```go
```

```go
```

```go
```

```go
```

```go
```

```go
```

```go
```

```go
```

```go
```

```go
```