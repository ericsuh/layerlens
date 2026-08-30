# Design Rationale

This application is built to help software engineers optimize building and syncing new versions of Docker images. Since Docker only reuses layers if the files are identical up to that point, inadvertant layer changes and unnecessary rebuilds can be easily introduced by configuration mistake, leading to longer build times, slower sync/deployments, and more disk and RAM use. A tool to analyze differences between different versions of an image allows an engineer to debug what might have caused such inadvertant changes.

## Problem choice

I chose this particular problem area because as a platform engineer in past jobs, I've wanted a tool like this to help optimize developer workflows and deployment, which meant that I could clearly articulate the requirements and minimum useful scope, as well as easily evaluate whether the application met the user needs. In addition, it has a large amount of data to explore, making the UI needs more complex than a simple chart could convey. There were interesting technical design choices around data handling and visualization that made this more than a trivial one-shot prompt for an agent, requiring some amount of iteration.

There is a related tool [`dive`](https://github.com/wagoodman/dive), a terminal UI (TUI) for exploring an image's layers, but it does not directly support the need expressed above, and so this is an unmet need requiring a new application.

For scope, I chose to exclude the following:

- Use cases that `dive` already handles, e.g. browsing images, analyzing potential wasted space.
- MacOS, Windows, and ARM images, because Linux/amd64 is the most common server deployment environment.
- More options to slice and dice the data (e.g. showing only files in specific layers, visualizing potential wasted space, showing file contents, having alternate file tree visualizations or navigaion, keyboard shortcuts) to keep the UI simple.

## Technical design

I decided to make this a web single-page app because there were many dimensions of data to convey (layer tree, filesystem paths, diff, sizes), which would be easier to comprehend and navigate in a GUI instead of a TUI, and I was most familiar with the idioms and needs of web apps as opposed to native GUI apps.

A subsantial amount of data handling and analysis would be required for the app, so I put that in the server backend, as I/O is simpler on the backend than in the browser. I chose Go for the backend toolchain because there are already some libraries for handling Docker image/registry interactions, Go is lower memory overhead to allow for more data to squeeze into a server VM, and naive Go code usually has faster I/O than compared to Python/Node.

I used Typescript + React + React Query on the client-side because I was most familiar with that stack and Redux seemed unnecessarily heavyweight for the small amount of mutable state in the app. I embedded the JS/CSS/HTML into the Go server app for ease of distribution and deployment.

In production, I might use a SQL database to store analysis results in order to have a shared-nothing architecture for the servers, but for this demo, I decided to use the filesystem for simplicity. I am only deploying one replica, I already needed to use the filesystem to unpack and analyze the image files, and I didn't require complex operations or data mutability, so it was natural to use the filesystem also for storing analysis results. In addition, using the filesystem makes it easier for someone (like me) to run this locally on their own machine to analyze Docker images they are troubleshooting locally.