# Адаптер native tinc: эксперимент этапа 0

Этот пакет запускает отдельный клиентский бинарь tinc, передаёт ему пакетный
дескриптор и обслуживает подготовку внешних сокетов. Он не подключён к текущему
`mobile/engine` и не меняет интерфейс Linux/Android приложения.

`ListenSocketGuard` принимает дубликат ровно одного unbound/unconnected TCP/UDP
socket через SCM_RIGHTS. Успешный callback подтверждается ACK 0; ошибки,
неправильные дескрипторы и закрытие не разрешают native-процессу использовать
сокет. Каждый полученный дескриптор закрывается. Callback на Android должен
выполнять и `VpnService.protect`, и привязку к выбранной физической Network.
Подготовка в обход пользовательского туннеля относится к внешним сокетам tinc,
а не к пользовательским DNS-запросам.

`Start` принимает локальную доверенную конфигурацию `DeviceType=fd`, `Device=3`,
передаёт дочернему процессу пакетный fd и собственный Unix guard. Истечение
разрешения или отмена контекста останавливают процесс и закрывают guard.
Собственный fd вызывающей стороны остаётся у неё. `Running` означает только
живой процесс и не подтверждает готовность туннеля.

Это низкоуровневый адаптер, а не публичный формат backend-конфигурации. Каталог
должен принадлежать текущему пользователю, иметь права 0700 и не быть symlink.
Пакетные дескрипторы должны сохранять границы IP-пакетов: TUN либо Unix
datagram/seqpacket socketpair. Произвольный конфиг пользователя, proxy commands
и исполняемые hooks сюда не передаются.

## Проверки

Из `mobile`:

```sh
go test -race ./tincbridge
go vet ./tincbridge
```

Для настоящего пути через четыре native-процесса сначала собрать отдельные
варианты из `mobile/native/tinc/README.md`, затем из `mobile`:

```sh
CLIENT_TINC_DAEMON="$PWD/../bin/client-tinc/host-Darwin-arm64-tcp-relay/tincd" \
CLIENT_TINC_CTL="$PWD/../bin/client-tinc/host-Darwin-arm64-tcp-relay/tinc" \
go test -count=1 -run TestNativeTincPacketFDThroughThreeServerLinks -v ./tincbridge
```

Подставьте каталог своей host-платформы. Без переменных native-тест явно
пропускается; unit-тесты выполняются независимо. В ограниченной среде нужны
права на локальные Unix/TCP listeners.

Изолированный Android harness находится в `android-spike`. Он упаковывает
бинарники в отдельный APK, чтобы соблюдать запрет target API29+ на исполнение
из writable app home. Он не пересобирает и не заменяет основное приложение.

```sh
CLIENT_TINC_ANDROID_VPN_GUARD=1 mobile/tincbridge/android-spike/run.sh
```

Команда выполняется из корня репозитория после сборки Android native tinc;
нужны SDK/NDK, JDK17, зависимости Gradle и запущенный ARM64-эмулятор. Harness
проверяет отдельно raw-IP packet fd, жизненный цикл и подготовку сокетов через
тестовый VpnService. Он не подтверждает готовность пользовательских
TCP/UDP/DNS и split policy через системный TUN основного приложения.
