.PHONY: project build release run clean

project:
	xcodegen generate

build: project
	xcodebuild -project RemoteFS.xcodeproj -scheme RemoteFS -configuration Debug -derivedDataPath .build build

release: project
	xcodebuild -project RemoteFS.xcodeproj -scheme RemoteFS -configuration Release -derivedDataPath .build build

run: build
	open .build/Build/Products/Debug/RemoteFS.app

clean:
	rm -rf .build
	rm -rf RemoteFS.xcodeproj
	rm -rf RemoteFSMenu/Resources
