# Design Rationale

## Theme and approach

I chose Theme 1 (Exploration & Understanding), because I am most interested in solving problems that result in useful products or artifacts.

I chose this particular problem area because as a platform engineer in past jobs, I've wanted a tool like this to help optimize developer workflows and deployment, which meant that I could clearly articulate the requirements and minimum useful scope, as well as easily evaluate whether the application met the user needs.

## The idea

This application helps software engineers optimize building and syncing new versions of Docker images.


Since Docker only reuses layers if the files are identical up to that point, inadvertant layer changes and unnecessary rebuilds can be easily introduced by configuration mistake, leading to longer build times, slower sync/deployments, and more disk and RAM use. A tool to analyze differences between different versions of an image allows an engineer to debug what might have caused such inadvertant changes.

## Why the idea is interesting

- **Solves a real problem**: This idea solves a real-world problem that I've encountered in previous jobs that has no pre-existing solutions. There is a related tool [`dive`](https://github.com/wagoodman/dive), but it does not directly support the need expressed above of comparing two versions of an image, and so this is an unmet need requiring a new application.

- **Nontrivial UI needs**: This idea requires visualizing filesystem data with multiple dimensions (layer tree, filesystem paths, diff, sizes) and data types to explore, making the UI and analytical needs more complex than one simple chart. To compare, `dive` is a TUI app, so compared to a GUI app, it is limited in its ability to explore richer dimensions of the data.

- **Nontrivial technical choices**: There were interesting choices around scope, data handling and storage, and visualization that made this more than a trivial one-shot prompt for an agent.

## Key decisions
- **Single-page web app**: I decided to make this a web single-page app (SPA), because a web GUI would better support the interactive visualization requirements than a TUI, and I was most familiar with the idioms and needs of web apps as opposed to native GUI apps. It was also easier to serve it as a web app for evaluation by someone else.

- **Server-side data analysis**: A substantial amount of data handling and analysis would be required for the app, so I put that in the server backend, as I/O is simpler on the backend than in the browser and aggregated data is more tractable as an API payload than full filesystem data. In addition, server-side data analysis would be easier to split off as a separate service later on.
- **Cached and precomputed analysis**: I had the data processing be mostly precomputed at fetch time, as that would make repeat analyses faster (as well as a substantially better first run/demo experience).

- **Go for backend toolchain**: I chose Go for the backend toolchain for several reasons:

  - Go has fast typechecking, testing, and linting tools for a better feedback cycle for the coding agent.
  - I knew there were already some Go libraries for handling Docker image/registry interactions.
  - Go is lower memory overhead to allow for more data to squeeze into a server VM's disk, since some images can be multiple GiB.
  - In my experience, it is easier to write fast I/O and compute code in Go than in Node/Python, and so I wanted to minimize potential performance pitfalls that the coding agent might fall into.
  - Go can be easily distributed as a single binary, for simpler deployments.
- **Typescript and React for frontend**: I chose Typescript and React for the frontend because there were widely used and reliable testing and static analysis tools that would provide a fast feedback cycle for the coding agent.
- **React Query for state**: I used React Query for state management because Redux seemed unnecessarily heavyweight for the small amount of mutable state in the app.
- **Analysis results stored in filesystem**: I used the filesystem instead of a SQL database for simplicity: I am only deploying one replica, I already needed to use the filesystem to unpack and analyze the image files, and I didn't require complex queries or data mutability. In addition, using the filesystem makes it easier for someone to run this app locally on their own machine to analyze Docker images they are troubleshooting.
- **Embedded web client code**: I embedded the JS/CSS/HTML into the Go server app for ease of distribution and deployment.
- **systemd for service management**: I used systemd to supervise the app on the server because I didn't need to scale this app up/down, the app was already simple to deploy, and without a container I didn't need to deal with another moving piece.
- **Registry allowlist**: I only added support for a handful of public known registries, so that I wouldn't need to handle private credentials and authentication and to prevent any SSRF attacks, though for a demo that would be unlikely.

For scope, I chose to exclude the following:

- Use cases that `dive` already handles, e.g. browsing images, analyzing potential wasted space.
- MacOS, Windows, and ARM images, because Linux/amd64 is the most common server deployment environment.
- More options to slice and dice the data (e.g. showing only files in specific layers, showing file contents, alternate file tree visualizations or navigaion, mutation or editing), because that would make the UI too complex for the scope of this project.

For directing the coding agent, I wrote up an initial [`PROJECT.md`](../.planning/PROJECT.md) document for the agent to consume, as I find that easier to reason through than editing a large prompt inline. I have the agent generate intermediate artifacts like `RESEARCH.md` and `ARCHITECTURE.md` so that I can audit at specific points during the project.

## Additional possible extensions

- Support for private registries
- Remote storage of analysis results to make web servers stateless and thus scale out better
- Split into data ingestion and analysis service and web app service for better scaling of each
- Suggested "fixes" for the Dockerfiles based on analyzed data
- Analysis of "wasted space" (i.e. files from earlier layers that are deleted in later layers)
- Accessibility and keyboard support
- CI/CD for the test server, plus releases to Github
- Actual documentation, usage guides
- Deeper security hardening (e.g. rate limiting, ensuring no user data gets executed)

## Approximate time spent

Around 4-5 hours of hands-on time.