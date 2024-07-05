To use this module, execute
```
go get github.com/deckhouse/MODULE/api@main. 
```

The pseudo-tag will be generated automatically.

Please note that model changes will NOT BE APPLIED in dependent modules until you rerun go get (the pseudo-tag points to a specific commit).
Therefore, it is important to remember to apply go get in all external modules that use this models.

!Also, DO NOT FORGET to update the models when the CRD are changed!