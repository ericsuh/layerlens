# Plan for development

We are building a web UI-based tool for analyzing differences between Docker images (e.g. two images, two different tags of an image, two different SHA256 hashes).

We will show both where layers are shared (and thus share a layer cache), which layers downstream *could* be shared, and what the differences are between different stacks (e.g. between the cumulative filesystem of layers 1-6 of one image and layers 1-5 of the other).

## Key requirements

This will be a web app server that shows the following main views.

### Image selection view

In the UI, the user should be able to select two images from:

- A list of prior downloaded, analyzed, and cached images that this app has made
- A list of images from a local Docker socket, if it exists
- User-inputted images from one of the known public registries (Docker Hub, Github Container Registry, Google Container Registry, Azure Container Registry, and AWS Elastic Container Registry). The server will fetch the image and analyze it. It can support up to 25 GiB images.

Once the user has selected the two images, they can move on to the next view.

### Layer browsing view

This view will have two main sections: a layer comparison section and a filesystem browsing section.

#### Layer comparison section

This comparison view will show the layers from the two images in a simple vertical tree, where there is a common "root" trunk that shows the shared layers (i.e. that would share a layer cache) followed by a fork and two branches for the layers that are only in one image or the other.

So this would look like:

```
1 -> 2 -> 3 -> 4a -> 5a
           \
            --> 4b -> 5b -> 6b
```

The layers should clearly indicate which image they belong to (or if they are shared), and what Dockerfile instruction(s) created that layer (use a tooltip/popover for really long instructions).

If any subsequent layers after the fork *could* be shared between the images (that is, the files added/removed in a layer of one image after the fork are the same as the changes in another layer in the other image), draw a dotted line that indicates that relationship.

The user should be able to select a layer in each image to compare in the filesystem section (or one layer in the shared trunk).

#### Filesystem diff section

A "diff" filesystem view that compares the cumulative filesystem of each image at a particular selected point in each images layer stack, e.g. comparing the files in layers 1-5 in image A and layers 1-6 in image B. The filesystem view should be a tree view that the user can both disclose (e.g. click a triangle next to a folder and have it open a subtree) as well as drill down into (open a folder to show just the files/folders in that folder). The cumulative filesystem includes a "squashing" of all layers from the beginning up to that selected layer, with whiteout being appropriately applied as file/folder deletions.

To keep the view compact, use a "unified" view that shows the deletions/additions between the two images as entries in the filesystem tree, colored and hatched appropriately.

For each folder, show the aggregate sizes of all files in that subtree, the number of files, and the changes in each due to the diff. For sizes, please show the sizes in a human readable number (e.g. 14.3 MiB, not the number in bytes). Also have a visual representation of the diff, like a mini horizontal bar showing the relative sizes of each entry in the tree view and layer view.

### Demo materials

We want to have some example images for a user to explore without having to supply their own. One should be a specific example of images from multiple builds of a Dockerfile where code changes or even extraneous files that weren't properly excluded by a `.dockerignore` file cause unnecessary layer cache invalidation.

```dockerfile
FROM node:24.04

WORKDIR /app
COPY . .
RUN npm install
RUN apt-get update -y && apt-get install -y ffmpeg && rm -rf /var/lib/{apt,dpkg,cache,log}/

CMD ["node", "./main.js"]
```

### Out of scope

- Multiplatform images (just choose linux/amd64)
- Windows images

## Prior Art

`wagoodman/dive` is very similar, but does not show a diff between two images, and it is a TUI rather than a visual web interface.

## Acceptance Criteria (for demo)

When starting the server, start off by downloading the pre-specified images (including the example images).

Golden workflow: user runs app, picks two example images from the list (`example:v1` and `example:v2`), sees layer tree view showing shared and separate layers, selects a layer from each branch, sees differences in the filesystem tree view as additions and removals of right image as compared to left image. User should 

## Technical design

Since this will be visualization heavy but also require access to Docker images and filesystems, structure the app as an HTTP server providing a JSON API that powers an SPA client.

### Backend

On the backend, use Go (latest) to power an HTTP server that will analyze the docker images. Connect to a local docker server/socket if one is available and then export the images to disk; otherwise, read from a specific folder on disk containing previously processed images like `/var/lib/layerlens/images`. Provide the data aggregated server-side (and paginated) where possible to reduce the size of the JSON payloads, such as aggregating by folder or for a certain depth. Use pre-existing libraries for interacting with Docker images, where possible (so that you can process the image data without relying on a Docker process).

Beware SSRF risks user-supplied docker image URLs; only query a pre-specified list. Cache image data and analysis durably in the local filesystem to speed up repeat analysis.

### Frontend

Write the UI in Typescript, bundled with esbuild and embedded in the Go app using `//go:embed`. Give the various parts of the UI space so that things don't look too crowded, and make it absolutely clear which elements are interactive using both the design of the elements, showing hover/interactivity animations and changing the mouse cursor. Always handle cases of text overflow where something might be too long, like filenames, layer instruction names, image names, sizes, etc. Use React and React Query for the frontend, since state is mostly ephemeral, and use an off-the-shelf UI component system for things like tables/trees, lists, buttons, etc. where possible.

### Testing, validation, toolchain

Use standard static analysis, typechecking, linting, and testing tools for both languages. Add unit tests for all important functionality, both on the server and on the client. Add playwright integration tests for end-to-end testing. Use mise as the dev toolchain manager and task runner.

All end-to-end tests will run locally on a Mac. Don't worry about getting things to run in CI/DinD.

### Deployment

The app will run on a Linux amd64 server on exe.dev, supervised by a systemd service to run it and keep it alive.

There should be a `mise run deploy` task that will transfer, via SSH, any systemd config files and the application to the server VM, and then use `systemctl` to start/restart the service.

## Workflow

Each of these steps should use a subagent/new context and reference prior steps via filesystem artifacts, such as markdown files or code. For research, detailed planning, and mockup, use Fable, but for other steps, use Opus.

1. In a Fable subagent, research prior art such as libraries for interacting with Docker image files, remote registries, to handle whiteout files, etc., as well as UI component libraries. Identify the best libraries that satisfy needs. Dump the research into a file called `.planning/DECISIONS.md`.
2. For anything that needs decision-making or clarification, ask the user, then note the responses in `.planning/RESEARCH.md`.
3. Come up with a UI design plan and put those in `.planning/DESIGN.md`, and then create a simple UI prototype for the major views.
3. Come up with a high level technical architecture, internal state/data schema and management (client and server-side), interaction flow, major module/API interfaces and abstractions, and overall test plan (including user acceptance tests) and put that in `.planning/ARCHITECTURE.md`.
5. Present both of the above to the user for approval.
6. Come up with a plan that slices implementation into phases, where each phase tackles a certain set of requirements, including more detailed implementation plans, test cases, etc. Record the overall plan in `.planning/IMPLEMENTATION.md` and details of each phase in files named like `.planning/IMPLEMENTATION-phase-001.md`. The overall plan should track status for each phase.
7. Then implement with a subagent per phase, ordered however makes sense, running tests and independent code review critiques, and fixing problems found. Make git commits as chunks of work are finished, and ensure the status of each phase is tracked in the plan.

If implementation reveals that any assumptions or decisions in previous files like `DESIGN.md` or `ARCHITECTURE.md` is wrong, update it and note the delta in `DECISIONS.md` before continuing.
