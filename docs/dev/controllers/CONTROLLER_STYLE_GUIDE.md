# Controller Style Guide

## Правила для написания контроллеров в проекте

Этот документ описывает стандарты и best practices для создания контроллеров в проекте. Следуйте этим правилам для обеспечения консистентности и соответствия review requirements.

---

## 1. Использовать стандартный `reconcile.Reconciler`

**⚠️ Не используйте без веской причины:**
- `reconcile.TypedReconciler[Request]` с кастомными типами запросов
- Кастомные `Request` интерфейсы и типы
- Типизированные очереди `workqueue.TypedRateLimitingInterface[TReq]`

**✅ Используйте по умолчанию:**
- Стандартный `reconcile.Reconciler` с `reconcile.Request`
- `reconcile.Request` содержит `NamespacedName` (Namespace и Name)
- Стандартные очереди `workqueue.RateLimitingInterface`

**Обоснование:** Стандартный reconciler достаточен для 99% задач. Типизированные версии добавляют сложность без существенных преимуществ. Используйте их только если есть реальная необходимость (например, если стандартный подход не позволяет решить задачу).

**Пример:**
```go
var _ reconcile.Reconciler = &Reconciler{}

func (r *Reconciler) Reconcile(
	ctx context.Context,
	req reconcile.Request,
) (reconcile.Result, error) {
	// Использовать req.NamespacedName для получения ресурса
	obj := &v1alpha3.SomeResource{}
	if err := r.cl.Get(ctx, req.NamespacedName, obj); err != nil {
		return reconcile.Result{}, err
	}
	// ...
}
```

---

## 2. Использовать `.For()` для основного ресурса

**⚠️ Не используйте без веской причины:**
- Только `.Watches()` без `.For()`
- `builder.TypedControllerManagedBy[TReq](mgr)`

**✅ Используйте по умолчанию:**
- `builder.ControllerManagedBy(mgr)` (стандартный builder)
- `.For(&ResourceType{})` перед `.Watches()` для указания основного ресурса
- Это стандартный паттерн controller-runtime

**Обоснование:** `.For()` явно указывает основной ресурс, который контроллер контролирует. Это делает код более читаемым и соответствует стандартам controller-runtime. Используйте только `.Watches()` без `.For()` только если контроллер не имеет основного ресурса (редкий случай).

**Пример:**
```go
err := builder.ControllerManagedBy(mgr).
	Named("controller_name").
	For(&v1alpha3.ReplicatedVolumeReplica{}).
	WithEventFilter(predicate.Funcs{
		// фильтры событий
	}).
	Complete(rec)
```

---

## 3. Не использовать сложные handlers без необходимости

**⚠️ Не используйте без веской причины:**
- `handler.EnqueueRequestForOwner`
- `handler.EnqueueRequestsFromMapFunc`
- `handler.TypedFuncs` с кастомными типами

**✅ Используйте по умолчанию:**
- Простые event handlers через `predicate.Funcs` для фильтрации событий
- `handler.EnqueueRequestForObject{}` если нужен простой enqueue (но обычно `.For()` достаточно)

**Обоснование:** Сложные handlers добавляют ненужную сложность. В большинстве случаев достаточно `.For()` с `predicate.Funcs` для фильтрации. Используйте сложные handlers только если есть реальная необходимость (например, для обработки owner references или сложной логики маппинга).

**Пример:**
```go
WithEventFilter(predicate.Funcs{
	CreateFunc: func(ce event.CreateEvent) bool {
		obj := ce.Object.(*v1alpha3.SomeResource)
		// логика фильтрации
		return shouldProcess
	},
	UpdateFunc: func(_ event.UpdateEvent) bool {
		return false // если не нужно обрабатывать
	},
	DeleteFunc: func(_ event.DeleteEvent) bool {
		return false // если не нужно обрабатывать
	},
	GenericFunc: func(ge event.GenericEvent) bool {
		// для reconciliation на старте
		return shouldProcess
	},
})
```

---

## 4. Использовать structured logger

**⚠️ Не используйте без веской причины:**
- `slog.Logger` напрямую в методах Reconcile
- Простые логи без контекста
- `r.log.Info(...)` без структурирования

**✅ Используйте по умолчанию:**
- `logr.Logger` с `.WithName()` и `.WithValues()` для структурированного логирования
- Создавать logger в начале метода `Reconcile` с контекстом запроса
- Использовать уровни логирования (`V(1)` для debug)

**Обоснование:** Structured logging улучшает трейсинг, отладку и мониторинг. Контекст запроса помогает отслеживать обработку конкретных ресурсов. Используйте `slog.Logger` напрямую только если есть специфические требования к логированию.

**Пример:**
```go
func (r *Reconciler) Reconcile(
	ctx context.Context,
	req reconcile.Request,
) (reconcile.Result, error) {
	log := r.logAlt.WithName("Reconcile").WithValues("req", req)
	log.Info("Reconciling")
	
	// ...
	
	log.Info("completed", "result", "success", "nodeID", nodeID)
	log.V(1).Info("debug info") // для debug уровня
	log.Error(err, "failed to process")
}
```

**Уровни логирования:**
- `log.Info()` - обычная информация (всегда видна)
- `log.V(1).Info()` - debug информация (требует увеличения verbosity)
- `log.Error()` - ошибки (всегда видна)

---

## 5. Обработка неиспользуемых параметров

**✅ Используйте:**
- Заменять неиспользуемые параметры на `_` в predicate functions
- Это предотвращает warnings от линтера

**Пример:**
```go
UpdateFunc: func(_ event.UpdateEvent) bool {
	// параметр не используется
	return false
},
DeleteFunc: func(_ event.DeleteEvent) bool {
	// параметр не используется
	return false
},
```

---

## Структура контроллера

### Файлы контроллера:

1. **`controller.go`** - регистрация контроллера
   - `BuildController(mgr manager.Manager) error`
   - Настройка builder с `.For()`, `.WithEventFilter()`, `.Complete()`
   - Инициализация Reconciler с зависимостями

2. **`reconciler.go`** - логика reconcile
   - `Reconciler` struct с зависимостями (`client.Client`, `client.Reader`, `*runtime.Scheme`, `*slog.Logger`, `logr.Logger`)
   - `Reconcile(ctx, req reconcile.Request)` метод
   - Вспомогательные методы для бизнес-логики

3. **`reconciler_test.go`** - unit тесты
   - Тесты с fake Kubernetes client
   - Использовать `reconcile.Request{NamespacedName: types.NamespacedName{Name: "name"}}`
   - Покрытие основных сценариев и edge cases

---

## Пример полного контроллера

### controller.go
```go
package mycontroller

import (
	"log/slog"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"github.com/deckhouse/sds-replicated-volume/api/v1alpha3"
	e "github.com/deckhouse/sds-replicated-volume/images/controller/internal/errors"
	u "github.com/deckhouse/sds-common-lib/utils"
)

func BuildController(mgr manager.Manager) error {
	rec := &Reconciler{
		cl:     mgr.GetClient(),
		rdr:    mgr.GetAPIReader(),
		sch:    mgr.GetScheme(),
		log:    slog.Default(),
		logAlt: mgr.GetLogger(),
	}

	err := builder.ControllerManagedBy(mgr).
		Named("my_controller").
		For(&v1alpha3.SomeResource{}).
		WithEventFilter(predicate.Funcs{
			CreateFunc: func(ce event.CreateEvent) bool {
				obj := ce.Object.(*v1alpha3.SomeResource)
				// логика фильтрации
				return obj.Spec.SomeField != ""
			},
			UpdateFunc: func(_ event.UpdateEvent) bool {
				return false // если не нужно обрабатывать
			},
			DeleteFunc: func(_ event.DeleteEvent) bool {
				return false // если не нужно обрабатывать
			},
			GenericFunc: func(ge event.GenericEvent) bool {
				obj := ge.Object.(*v1alpha3.SomeResource)
				return obj.Spec.SomeField != ""
			},
		}).
		Complete(rec)

	if err != nil {
		return u.LogError(rec.log, e.ErrUnknownf("building controller: %w", err))
	}

	return nil
}
```

### reconciler.go
```go
package mycontroller

import (
	"context"
	"log/slog"
	"github.com/go-logr/logr"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"github.com/deckhouse/sds-replicated-volume/api/v1alpha3"
)

type Reconciler struct {
	cl     client.Client
	rdr    client.Reader
	sch    *runtime.Scheme
	log    *slog.Logger
	logAlt logr.Logger
}

var _ reconcile.Reconciler = &Reconciler{}

func (r *Reconciler) Reconcile(
	ctx context.Context,
	req reconcile.Request,
) (reconcile.Result, error) {
	log := r.logAlt.WithName("Reconcile").WithValues("req", req)
	log.Info("Reconciling")
	
	obj := &v1alpha3.SomeResource{}
	if err := r.cl.Get(ctx, req.NamespacedName, obj); err != nil {
		if client.IgnoreNotFound(err) == nil {
			log.V(1).Info("resource not found, might be deleted")
			return reconcile.Result{}, nil
		}
		return reconcile.Result{}, err
	}
	
	// бизнес-логика
	
	log.Info("completed successfully")
	return reconcile.Result{}, nil
}
```

### reconciler_test.go
```go
package mycontroller_test

import (
	"context"
	"testing"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"github.com/deckhouse/sds-replicated-volume/api/v1alpha3"
)

func TestReconcile(t *testing.T) {
	ctx := context.Background()
	obj := &v1alpha3.SomeResource{
		ObjectMeta: metav1.ObjectMeta{Name: "test-resource"},
	}
	cl := fake.NewClientBuilder().WithObjects(obj).Build()
	rec := NewReconciler(cl, cl, scheme.Scheme, slog.Default(), logr.Discard())
	
	req := reconcile.Request{
		NamespacedName: types.NamespacedName{Name: "test-resource"},
	}
	_, err := rec.Reconcile(ctx, req)
	
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}
```

---

## Чеклист при создании контроллера

- [ ] Использовать стандартный `reconcile.Reconciler` (не TypedReconciler)
- [ ] Использовать `builder.ControllerManagedBy` (не TypedControllerManagedBy)
- [ ] Использовать `.For(&ResourceType{})` для основного ресурса
- [ ] Использовать `predicate.Funcs` для фильтрации событий
- [ ] Использовать structured logger с `.WithName()` и `.WithValues()` в Reconcile
- [ ] Заменять неиспользуемые параметры на `_` в predicate functions
- [ ] Не использовать сложные handlers без необходимости
- [ ] Добавить unit тесты с fake client
- [ ] Использовать `reconcile.Request` в тестах
- [ ] Зарегистрировать контроллер в `registry.go`

---

## Регистрация контроллера

После создания контроллера, зарегистрируйте его в `images/controller/internal/controllers/registry.go`:

```go
import (
	mycontroller "github.com/deckhouse/sds-replicated-volume/images/controller/internal/controllers/my_controller"
)

func init() {
	registry = append(registry, mycontroller.BuildController)
	// ...
}
```

---

## Дополнительные рекомендации

### Обработка ошибок
- Использовать типизированные ошибки из `internal/errors`
- Возвращать `reconcile.Result{}` с ошибкой для retry
- Логировать ошибки через structured logger

### Конфликты при обновлении
- Использовать `api.PatchStatusWithConflictRetry` или `api.PatchWithConflictRetry` для безопасных обновлений
- Эти функции обрабатывают конфликты через optimistic locking

### Идемпотентность
- Reconcile должен быть идемпотентным
- Проверять текущее состояние перед изменениями
- Не делать лишних обновлений, если состояние уже корректное

### Параллельная обработка (обсуждаемо)

**⚠️ Обсуждаемо:** Поддержка параллельной обработки через goroutines.

**Текущий подход:**
- Controller-runtime по умолчанию обрабатывает запросы параллельно (несколько воркеров)
- Используйте `PatchStatusWithConflictRetry` / `PatchWithConflictRetry` для безопасных обновлений
- Эти функции используют optimistic locking и автоматически обрабатывают конфликты

**Возможные улучшения (требуют обсуждения):**
- Явное управление параллелизмом через goroutines для независимых операций
- Использование worker pools для обработки множества ресурсов
- Параллельная обработка нескольких ресурсов одного типа

**Рекомендация:** Начинайте с стандартного подхода controller-runtime. Рассматривайте явную параллелизацию только если:
- Есть доказанная необходимость (производительность)
- Операции действительно независимы
- Обсуждено с командой

**Пример безопасной параллельной обработки (обсуждаемо):**
```go
// Внутри Reconcile, если нужно обработать несколько ресурсов параллельно
// ⚠️ Используйте только после обсуждения с командой
var wg sync.WaitGroup
errCh := make(chan error, len(resources))

for _, resource := range resources {
    wg.Add(1)
    go func(r Resource) {
        defer wg.Done()
        if err := processResource(ctx, r); err != nil {
            errCh <- err
        }
    }(resource)
}

wg.Wait()
close(errCh)

// Проверить ошибки
for err := range errCh {
    if err != nil {
        return reconcile.Result{}, err
    }
}
```

**Важно:** При использовании goroutines убедитесь, что:
- Операции действительно независимы
- Нет race conditions при доступе к общим ресурсам
- Используется правильная обработка ошибок и контекста
- Это обсуждено с командой перед реализацией

### Тестирование
- Покрывать основные сценарии (happy path)
- Покрывать edge cases (ошибки, граничные условия)
- Использовать fake client для изоляции тестов
- Тестировать идемпотентность

---

## Ссылки

- [controller-runtime documentation](https://pkg.go.dev/sigs.k8s.io/controller-runtime)
- [Kubebuilder book](https://book.kubebuilder.io/)
- Спецификация контроллеров: `docs/dev/spec_v1alpha3.md`

