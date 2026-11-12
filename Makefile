.PHONY: test vet test-python mobile-engine mobile-apk linux windows macos
test:
	go test -timeout=180s ./...
	go -C mobile test -timeout=180s ./...
vet:
	go vet ./...
	go -C mobile vet ./...
test-python:
	python3 -m unittest discover -s mobile/scripts -p 'test_*.py'
	cd mobile/linux/ui && python3 -m unittest test_platform_runtime
	python3 -m unittest discover -s mobile/windows/tests -p 'test_*.py'
	@if [ "$$(uname -s)" = Linux ]; then python3 -m unittest discover -s mobile/native/tinc/tests -p 'test_*.py'; fi
mobile-engine:
	./mobile/scripts/build-engine.sh
mobile-apk: mobile-engine
	cd mobile/android && ./gradlew assembleDebug
linux:
	./mobile/scripts/build-linux-client.sh all
windows:
	./mobile/scripts/build-windows-client.sh
macos:
	./mobile/scripts/build-macos-client.sh
