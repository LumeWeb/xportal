---
"@lumeweb/xportal": patch
---

-   Documentation update renaming the "account" plugin to "dashboard"
-   Added Docker build process and base image creation
-   Fixed Dockerfile to use a copy of the Golang image as the final image
-   Refactored to remove version check as Portal does not yet support it