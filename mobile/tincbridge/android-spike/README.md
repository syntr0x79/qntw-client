# Изолированная проверка tinc на Android

Отдельный пакет `link.quantway.tincspike` запускает тесты `mobile/tincbridge` под
обычным Android app UID с target SDK 35. Основное приложение Quantway и его
Gradle-проект не меняются. У тестового пакета нет пользовательских настроек или
подписок.

Нужны запущенный ARM64 AVD (`emulator-5554` по умолчанию), Android SDK 35, NDK
28.0.13004108, JDK 17, Go и кеш существующего Android Gradle wrapper/AGP 8.9.2.
Также нужна собранная native-версия tinc из
`bin/client-tinc/android-arm64-v8a-tcp-relay`.

Из корня репозитория:

```sh
CLIENT_TINC_ANDROID_VPN_GUARD=1 mobile/tincbridge/android-spike/run.sh
```

Скрипт сам собирает Go test executable и NDK fixture. Все подготовленные
библиотеки, Gradle build/cache, APK и журнал instrumentation находятся в
`/private/tmp/clienttinc-android-spike`. В APK включаются `libtinctest.so`,
`libtincctl.so`, `libtincd.so` и `libfixture.so`; выполняются файлы из
`nativeLibraryDir`, а не из writable app storage. Тестовые Unix-сокеты и
конфигурации создаются в закрытом app cache. Скрипт проверяет результаты native
процесса и instrumentation: нулевой exit code команды adb сам по себе не
считается успехом.

Режим `CLIENT_TINC_ANDROID_VPN_GUARD=1` разрешает VPN только тестовому пакету
через `appops ACTIVATE_VPN` и запускает отдельный foreground VpnService. До
создания VPN выбирается физическая `Network`; создаётся один TUN с
представительным маршрутом `198.18.71.0/24`. Через abstract LocalServerSocket
передаются дубликаты native-сокетов, для которых вызываются реальные
`VpnService.protect` и `Network.bindSocket`. Ответ ACK0 выдаётся только после
успеха обоих вызовов. В конце закрываются TUN, broker и сервис.

Это доказывает работу native socket guard при активном Android VPN и передачу
сырых пакетов через отдельный packet-FD стенда. Пользовательские пакеты через
системный TUN в этой проверке не передаются; полноценная маршрутизация
пользовательского VPN этим тестом не подтверждается. Отключение guard-режима
оставляет только packaged native-тесты без запуска системного VPN.

Параметры окружения:

- `ANDROID_SERIAL` — выбранный эмулятор.
- `CLIENT_TINC_ANDROID_SPIKE_OUT` — отдельный каталог результатов.
- `CLIENT_TINC_ANDROID_TEST` — готовый Android executable вместо автоматической сборки.
- `CLIENT_TINC_ANDROID_BIN` — каталог с native `tinc` и `tincd`.
- `CLIENT_TINC_ANDROID_TEST_RUN` — фильтр имён Go-тестов.
- `ANDROID_HOME`, `ANDROID_NDK_HOME`, `JAVA_HOME` — пути инструментов.
