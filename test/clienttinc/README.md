# Disposable native socket fixture

This is client-side fixture code, not an installed application helper.
Build explicitly for an isolated Linux stand:

```sh
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go -C mobile build -o ../bin/guarded-native-fixture ../test/clienttinc/guarded_native_main_linux.go
```

The backend-owned database/API integration stands remain in qntw-backend.
They consume client build artifacts through explicit fixture paths.
