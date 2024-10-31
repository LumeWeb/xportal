# @lumeweb/xportal

## 0.2.4

### Patch Changes

- 0ba0b86: Dummy version bump because proxy.golang.org has badly cached v0.2.3

## 0.2.3

### Patch Changes

- 1117c28: update dependencies

## 0.2.2

### Patch Changes

- f18ca0d: we need to call newGoGenerateCommand not newGoModCommand

## 0.2.1

### Patch Changes

- 99c1000: update dependencies

## 0.2.0

### Minor Changes

- 24c4684: - Remove replacement for github.com/go-co-op/gocron/v2
  - Add a scratch mode that will generate the environment in the parent directory
  - Bump github.com/Masterminds/semver/v3 from 3.2.1 to 3.3.0
  - Bump github.com/josephspurrier/goversioninfo from 1.4.0 to 1.4.1
  - Integrate Cobra for improved command-line interface
  - Add support for go generate for plugin/libraries by calling it globally
  - Include various backports

## 0.1.5

### Patch Changes

- b17eb6d: Addressed issues related to templates and dependencies.

## 0.1.4

### Patch Changes

- 6aed372: vendor dependencies in debug node

## 0.1.3

### Patch Changes

- b4b28e8: - Documentation update renaming the "account" plugin to "dashboard"
  - Added Docker build process and base image creation
  - Fixed Dockerfile to use a copy of the Golang image as the final image
  - Refactored to remove version check as Portal does not yet support it

## 0.1.2

### Patch Changes

- a37fc52: Missing the default service implementation in the module template

## 0.1.1

### Patch Changes

- e83419a: Update module template to remove standard import preset

## 0.1.0

### Minor Changes

- b4abecb: Initial version
