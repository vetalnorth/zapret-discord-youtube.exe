name: Build Zapret Launcher

on:
  push:
    branches: [ "main" ]
  workflow_dispatch:

jobs:
  build:
    runs-on: windows-latest

    steps:
    - name: Checkout repository
      uses: actions/checkout@v4

    - name: Set up Go
      uses: actions/setup-go@v5
      with:
        go-version: '1.22'

    - name: Build Launcher
      run: |
        go build -ldflags="-s -w" -o zapret-launcher.exe main.go

    - name: Pack Full Distribution
      run: |
        mkdir dist
        copy zapret-launcher.exe dist\
        xcopy bin dist\bin\ /E /I /Y
        xcopy lists dist\lists\ /E /I /Y
        copy README.md dist\
        compress-archive -Path dist\* -DestinationPath zapret-launcher-windows.zip

    - name: Upload Artifact
      uses: actions/upload-artifact@v4
      with:
        name: zapret-launcher-windows
        path: zapret-launcher-windows.zip
