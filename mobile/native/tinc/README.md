# Native tinc клиентского слоя

## Форк для macOS и Linux стенда

macOS host-сборка использует `TINC_FORK_COMMIT` из `source-pin.env` и Meson.
Старые socket guard, TCP relay и SPTPS patches уже включены в форк и повторно
не применяются. Клиентский профиль: SPTPS-only, обязательный socket guard,
отключённые hooks, UDP batch 1 и offload off. Служебный mesh парка эта сборка
не меняет.

Нужны C toolchain, Git, Meson 1.9.1 и Ninja 1.13.0 (допускается суффикс
официального PyPI wheel `git.kitware.jobserver-pipe-1`). Утилиты можно установить
в отдельное Python virtualenv и передать его `bin` через `PATH`; либо задать
`TINC_MESON_BIN`/`TINC_NINJA_BIN`.

```sh
TINC_SOURCE_REPOSITORY=/path/to/fork mobile/scripts/build-client-tinc.sh host
```

Результат на Apple Silicon: `bin/client-tinc/host-Darwin-arm64-fork/`.
`BUILD-INFO.txt` содержит точный commit, профиль, toolchain и SHA-256 бинарей.
`SHA256SUMS` проверяет комплект; `tinc-source.tar.gz` содержит соответствующие
исходники. `prepare-tinc-fork.sh` извлекает только закреплённый commit через
`git archive`, независимо от HEAD и незакоммиченных изменений checkout.
`TINC_CLIENT_BUILD_DIR` и `TINC_CLIENT_OUTPUT_DIR` задают отдельные каталоги.

Для изолированного Linux-клиента стенда нужен Docker; сборка явно выбирается:

```sh
TINC_CLIENT_SOURCE=fork TINC_SOURCE_REPOSITORY=/path/to/fork \
  mobile/scripts/build-tinc-linux.sh arm64
```

Результат — `bin/quantway-tinc-linux-fork/linux-arm64/`. В контейнере закреплены
Meson 1.9.1 и Alpine Ninja 1.12.1-r0. Для второго target доступен `amd64`.
Полный источник передаётся Docker маленьким отдельным контекстом без приложения
и его учётных данных. Android, Windows и обычная Linux desktop/DEB-сборка пока
сохраняют прежний pin и сборщики; их перенос выполняется отдельно. Без
`TINC_CLIENT_SOURCE=fork` Linux wrapper использует legacy-путь.

## Прежний прототип Android и desktop

Следующие сведения относятся к legacy-сборке этапа 0. Для её host-варианта
используйте `build-client-tinc-legacy.sh host`; команда `build-client-tinc.sh host`
уже собирает описанный выше форк.

Это сборка для этапа 0 из
`docs/superpowers/plans/2026-10-31-client-tinc-layer.md`. Она не подключена к
приложению и не заменяет сборку служебного меша парка.

## Источник и сборка

`source-pin.env` закрепляет upstream commit tinc, Android NDK и API. Сборка
извлекает только закоммиченный upstream source через `git archive`, даже если
переданный локальный checkout содержит изменения. Каждый запуск получает новый
каталог, повторно применяет проверенные патчи и сохраняет `BUILD-INFO.txt` с их
SHA-256, параметрами и суммами бинарей. `SOURCE_DATE_EPOCH` задаётся временем
закреплённого коммита, пути временного каталога исключены из debug info.
Побитовая воспроизводимость разными версиями host-toolchain пока
не заявляется.

Из корня репозитория:

```sh
export ANDROID_NDK_HOME="$HOME/Library/Android/sdk/ndk/28.0.13004108"
mobile/scripts/build-client-tinc.sh android arm64-v8a
mobile/scripts/build-client-tinc-legacy.sh host
```

Нужны Git, Python 3, autoconf, automake, make и C toolchain. Android сборка
использует NDK без Docker. Вместо загрузки можно передать Git checkout:

```sh
TINC_SOURCE_REPOSITORY=/path/to/tinc-checkout mobile/scripts/build-client-tinc-legacy.sh host
```

Результат по умолчанию:
`bin/client-tinc/android-arm64-v8a-tcp-relay/{tincd,tinc}` или
`bin/client-tinc/host-Darwin-arm64-tcp-relay/{tincd,tinc}`. Допустимы Android ABI
`arm64-v8a`, `x86_64`, `armeabi-v7a`, `x86`; проверенные на этой машине ABI
перечисляются в отчёте, а наличие ветки в скрипте само по себе не является
проверкой. `TINC_CLIENT_BUILD_DIR` и `TINC_CLIENT_OUTPUT_DIR` задают отдельные
каталоги работы и результата.

## Сокеты и протокол callback

Клиентская сборка требует `TINC_SOCKET_GUARD` — абсолютный filesystem-путь
к Unix STREAM socket в приватном каталоге приложения. Все прямые вызовы
`socket()` из кода tinc проходят общий guard, включая TCP listeners,
исходящие TCP, UDP и временные сокеты ICMP. Созданный AF_INET/AF_INET6 fd
передаётся приложению **до bind/connect/listen/отправки**. Все остальные
сетевые семейства кроме AF_UNIX запрещены.

1. На каждое создание внешнего сокета tinc подключается к Unix broker.
2. `sendmsg` передаёт byte `0x01` и ровно один fd в `SCM_RIGHTS`.
3. Broker получает дубликат исходного unbound/unconnected сокета. На Android
   он вызывает `VpnService.protect` и `Network.bindSocket` физической сети.
4. ACK `0x00` разрешает tinc использовать исходный fd; любой иной ответ,
   закрытие broker или общий таймаут 5 секунд закрывает fd и возвращает ошибку.
5. Broker закрывает свой дубликат после обработки; tinc владеет исходным fd.

Чистый host callback в unit-тестах проверяет только протокол, а не Android
protect. Go broker и адаптер процесса находятся отдельно в `mobile/tincbridge`.
Отдельный Android harness также проверил реальный `VpnService.protect` и
`Network.bindSocket` в AVD; эта проверка не интегрирует режим в текущее приложение.

Принимаемый TCP socket наследует сетевой контекст подготовленного listener.
Его нельзя повторно передавать в `Network.bindSocket`: он уже подключён.
Наследование Android protect/Network для accepted sockets нужно подтвердить
платформенным тестом до включения входящих соединений клиента. Локальные
AF_UNIX sockets не требуют физической сетевой подготовки.

Сборка принимает только числовые внешние адреса и порты: wrapper
`getaddrinfo/getnameinfo` принудительно включает numeric flags. DNS имён входов
должен выполнять отдельный разрешённый адаптер выбранной физической сети.
Hostname, строковое имя порта или обратный DNS не обходят этот адаптер через
libc. Контролируемая конфигурация использует `DeviceType=fd`, `AutoConnect=no`,
`LocalDiscovery=no`; backend-конфигурация не является произвольным исполняемым
файлом от пользователя.

## Пакетный интерфейс

Upstream `DeviceType=fd` поддерживает дескриптор `Device=3` либо его получение
по Unix socket. Он читает и пишет **сырые IPv4/IPv6 пакеты без Ethernet
заголовка**. Внутри tinc синтетический Ethernet заголовок добавляется и
снимается адаптером. Подходят TUN или `socketpair(AF_UNIX, SOCK_DGRAM/SEQPACKET)`
с сохранением границ пакетов. Обычный STREAM fd не подходит.

Это позволяет оставить одного владельца системного TUN, но ещё не доказывает
перенос пользовательских TCP/UDP/DNS, split policy и внутреннего туннеля до
назначенного выхода через Android VpnService.

## Изоляция и ограничения этапа 0

Для стенда выбран отдельный tinc island для каждого устройства: в нём только
само устройство и его серверные входы. Входы передают адресованный внутренний
авторизованный поток в отдельный серверный core mesh. Клиент не получает ключи
и topology core, а разные устройства не участвуют в одном tinc-графе.

Изоляцию всё равно проверяют на gateway: допустимые source/destination,
сервис, устройство и назначенный выход. Эта схема не масштабируется бесплатно:
число edge-процессов и соединений растёт как устройства × входы. Она допустима
как малый прототип; целевой production масштаб и альтернативы сегментации
требуют измерений и отдельного решения.

Обычный общий tinc-граф и `Forwarding=off` на штатном приложении не заменяют
серверного запрета клиентского транзита. Native binaries сами по себе не
реализуют выдачу доступа, ACL, назначение выхода, учёт или изоляцию.

## Проверки

```sh
python3 mobile/native/tinc/tests/test_socket_guard.py
```

Эти проверки используют реальные Unix sockets и передачу дескрипторов:
unbound/unconnected IPv4 TCP/UDP и IPv6, отсутствие/отказ/закрытие broker,
ограниченный таймаут и запрет DNS lookup. Ограниченная среда запуска может
потребовать разрешения на локальный Unix listener.

TCP multihop patch сохранён отдельно в
`deploy/tinc-client/patches/0001-relay-existing-sptps-records-over-tcp.patch`.
Он включён по умолчанию **только в эту экспериментальную клиентскую сборку**
после сравнения реального пути нескольких узлов до/после патча:

- Host packet-FD через три TCP-связи: без патча пакеты не доходили за 25 секунд;
  с патчем двунаправленная передача прошла за 5,80 секунды.
- Android packet-FD с настоящим VpnService: передача по трём связям прошла за
  0,86 секунды; подготовлены 11 внешних сокетов без отказов callback.
- Linux stand принудительно оставил клиенту единственный вход i1, заблокировал
  прямую TCP/UDP-связь i1 с A1 в обоих направлениях и подтвердил промежуточный
  сервер. Без патча TCP-передача не проходила; с ним полный набор проверок
  TLS/HTTP/UDP DNS, изоляции и смены выхода прошёл.

Эти времена — наблюдения отдельных прогонов, не обещание задержки подключения.
Существующая сборка `deploy/tinc` и `make tinc-linux` не менялись.

Для воспроизведения исходного отказа остаётся явная сборка без патча:

```sh
TINC_CLIENT_TCP_RELAY_PATCH=0 mobile/scripts/build-client-tinc-legacy.sh host
TINC_CLIENT_TCP_RELAY_PATCH=0 mobile/scripts/build-client-tinc.sh android arm64-v8a
```

Её отдельные каталоги **не имеют** суффикса `-tcp-relay`, например
`bin/client-tinc/host-Darwin-arm64/`; они не перезаписывают обычный patched
результат. В `BUILD-INFO.txt` записан флаг, а при включении — SHA-256 патча.

## Лицензия

`COPYING` скопирован из закреплённого upstream commit. Tinc и изменения guard
распространяются по GPL-2.0-or-later. Сборка сохраняет лицензию рядом с бинарями;
упаковка приложения с соответствующими исходниками относится к следующему
платформенному этапу.
