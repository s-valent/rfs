.PHONY: project server build release run clean

project:
	xcodegen generate

build: project
	xcodebuild -project RemoteFSMenu.xcodeproj -scheme RemoteFSMenu -configuration Debug -derivedDataPath .build build

release: project
	xcodebuild -project RemoteFSMenu.xcodeproj -scheme RemoteFSMenu -configuration Release -derivedDataPath .build build

run: build
	open .build/Build/Products/Debug/RemoteFSMenu.app

clean:
	rm -rf .build
	rm -rf RemoteFSMenu.xcodeproj
	rm -rf RemoteFSMenu/Resources
