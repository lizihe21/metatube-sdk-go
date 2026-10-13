# MetaTube SDK Go

[![Build Status](https://img.shields.io/github/actions/workflow/status/metatube-community/metatube-sdk-go/docker.yml?branch=main&style=flat-square&logo=github-actions)](https://github.com/metatube-community/metatube-sdk-go/actions/workflows/release.yml)
[![Go Linter](https://img.shields.io/github/actions/workflow/status/metatube-community/metatube-sdk-go/linter.yml?style=flat-square&branch=main&logo=githubactions&label=golangci-lint)](https://github.com/metatube-community/metatube-sdk-go/actions/workflows/linter.yml)
[![Require Go Version](https://img.shields.io/badge/go-%3E%3D1.26-30dff3?style=flat-square&logo=go)](https://github.com/metatube-community/metatube-sdk-go/blob/main/go.mod)
[![GitHub License](https://img.shields.io/github/license/metatube-community/metatube-sdk-go?color=e4682a&logo=apache&style=flat-square)](https://github.com/metatube-community/metatube-sdk-go/blob/main/LICENSE)
[![Tag](https://img.shields.io/github/v/tag/metatube-community/metatube-sdk-go?color=%23ff8936&logo=fitbit&style=flat-square)](https://github.com/metatube-community/metatube-sdk-go/tags)

Metadata Tube SDK in Golang.

## Contents

- [MetaTube SDK Go](#metatube-sdk-go)
    - [Contents](#contents)
    - [Features](#features)
    - [Installation](#installation)
    - [Credits](#credits)
    - [License](#license)

## Features

- Supported platforms
    - Linux
    - Darwin
    - Windows
    - BSD(s)
- Supported Databases
    - [SQLite](https://gitlab.com/cznic/sqlite)
    - [PostgreSQL](https://github.com/jackc/pgx)
- Image processing
    - Auto cropping
    - Badge support
    - Face detection
    - Image hashing
- RESTful API
- 20+ providers
- Text translation

## Installation

To install this package, you first need [Go](https://golang.org/) installed (**Go 1.26+ is required**), then you can use
the below Go command to install SDK.

```sh
go get -u github.com/metatube-community/metatube-sdk-go
```

## New provider and translation capabilities (development branch)

This branch introduces JavLibrary and JavDB movie providers with a
shared request budget, an optional connector to an **existing**
FlareSolverr Docker HTTP API, and enhanced OpenAI-compatible translation
(Chat Completions / Responses, JSON / streamed SSE). Features remain
unmerged until the draft PR is reviewed and verified.

- [JavLibrary / JavDB configuration and FlareSolverr API](docs/providers-javlibrary-javdb.md)
- [OpenAI translation protocol and options](docs/openai-translation.md)
- [Offline vs. real-network testing, skip gates and release checklist](docs/test-matrix.md)

Offline tests run without external accounts, websites, or Docker:

```sh
make test
make test-vet
make test-race
```

All real-site and external API checks are deliberately **opt-in**.
See the test matrix for the required variables and per-provider commands.
A skipped live check means **not verified**, not a successful test.

## Credits

| Library														                                           | Description																						                                                                    |
|-----------------------------------------------------------------|------------------------------------------------------------------------------------------------------|
| [gocolly/colly](https://github.com/gocolly/colly)			            | Elegant Scraper and Crawler Framework for Golang													                                        |
| [gin-gonic/gin](https://github.com/gin-gonic/gin)			            | Gin is a HTTP web framework written in Go															                                             |
| [gorm.io/gorm](https://gorm.io/)								                        | The fantastic ORM library for Golang																                                                 |
| [esimov/pigo](https://github.com/esimov/pigo)				               | Fast face detection, pupil/eyes localization and facial landmark points detection library in pure Go |
| [robertkrimen/otto](https://github.com/robertkrimen/otto)       | A JavaScript interpreter in Go (golang)                                                              |
| [modernc.org/sqlite](https://gitlab.com/cznic/sqlite)		         | Package sqlite is a CGo-free port of SQLite/SQLite3												                                      |
| [corona10/goimagehash](https://github.com/corona10/goimagehash) | Go Perceptual image hashing package																                                                  |
| [antchfx/xpath](https://github.com/antchfx/xpath)			            | XPath package for Golang, supports HTML, XML, JSON document query									                           |
| [gen2brain/jpegli](https://github.com/gen2brain/jpegli)         | Go encoder/decoder for JPEG based on jpegli                                                          |

## License

[Apache-2.0 License](https://github.com/metatube-community/metatube-sdk-go/blob/main/LICENSE)
