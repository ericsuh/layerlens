/**
 * Golden payloads captured from the running phase-005 server, so the component
 * tests assert against the real wire shape rather than a hand-written guess.
 * Regenerate with:
 *   curl -s "localhost:8080/api/v1/diff/layers?left=<v1>&right=<v2>"
 */
import type { ImagesResponse, LayerGraph, TreePage } from "./api/types";

export const GOLDEN_GRAPH: LayerGraph = {
  "left": {
    "id": "sha256:7c354f60d198fee7ba6ce2df9cead2a74748de8cb3a1a6049b63727a7f6d3fc8",
    "refNames": [
      "example:v1"
    ],
    "source": "fixture",
    "platform": "linux/amd64",
    "layerCount": 8,
    "totalBytes": 74074241,
    "createdAt": "2026-08-27T14:02:00Z",
    "ingestedAt": "2026-08-29T23:39:16.065663522Z",
    "pinned": true
  },
  "right": {
    "id": "sha256:2b27ab1608ab31a265a7265e982e90aeb558205e8cb4e077fde0543c8bac3c0e",
    "refNames": [
      "example:v2"
    ],
    "source": "fixture",
    "platform": "linux/amd64",
    "layerCount": 8,
    "totalBytes": 79067477,
    "createdAt": "2026-08-29T09:41:00Z",
    "ingestedAt": "2026-08-29T23:39:16.087870397Z",
    "pinned": true
  },
  "trunkLength": 5,
  "trunk": [
    {
      "index": 0,
      "diffId": "sha256:393276d913ee3964285a13a936ebad3b884a52b82fad001db2da237bcf5018f0",
      "chainId": "sha256:393276d913ee3964285a13a936ebad3b884a52b82fad001db2da237bcf5018f0",
      "compressedDigest": "sha256:746066287423be2382128adb2392d83209ccd84b72e0b3c3d8a2450728d08789",
      "compressedSize": 27669,
      "contentBytes": 18342917,
      "entryCount": 192,
      "instruction": "ADD file:4d192565a7220e135cab8c6c9d3e6f2f8e4b5a6c7d8e9f0a1b2c3d4e5f6a7b8 in /",
      "instructionRaw": "/bin/sh -c #(nop) ADD file:4d192565a7220e135cab8c6c9d3e6f2f8e4b5a6c7d8e9f0a1b2c3d4e5f6a7b8 in / ",
      "instructionKnown": true,
      "owner": "shared"
    },
    {
      "index": 1,
      "diffId": "sha256:adc0091108675d44e90210b076348b8728a33d1dc07a5cc1a1448eeb35ded600",
      "chainId": "sha256:4e20573fc6bfa3d0af17266b010a7f77c4ef04a6f44bea755a5acf0aaf46e784",
      "compressedDigest": "sha256:75e591dbff762f45039b4cbcbd2fe40914778e3bd8232b2cd922af7a97c72e9c",
      "compressedSize": 21217,
      "contentBytes": 16630252,
      "entryCount": 105,
      "instruction": "RUN set -eux; apt-get update; apt-get install -y --no-install-recommends ca-certificates curl git wget gnupg dirmngr xz-utils libatomic1",
      "instructionRaw": "RUN /bin/sh -c set -eux; apt-get update; apt-get install -y --no-install-recommends ca-certificates curl git wget gnupg dirmngr xz-utils libatomic1 # buildkit",
      "instructionKnown": true,
      "owner": "shared"
    },
    {
      "index": 2,
      "diffId": "sha256:555cde80761a711d18b5cd5d11202be5c47214368134c60e5e1e14196b118b4f",
      "chainId": "sha256:48cbfbb179d4d62a2ed63831ae9f8543e8cdea678d6b191cbc8c14120c544237",
      "compressedDigest": "sha256:815e2a5d27e14c11defc65001cc25614f4e21ba8742a95603f772f425c3f495f",
      "compressedSize": 22207,
      "contentBytes": 16293264,
      "entryCount": 172,
      "instruction": "RUN ARCH= && dpkgArch=\"$(dpkg --print-architecture)\" && curl -fsSLO --compressed \"https://nodejs.org/dist/v24.4.0/node-v24.4.0-linux-x64.tar.xz\" && tar -xJf \"node-v24.4.0-linux-x64.tar.xz\" -C /usr/local --strip-components=1 --no-same-owner && ln -s /usr/local/bin/node /usr/local/bin/nodejs",
      "instructionRaw": "RUN /bin/sh -c ARCH= && dpkgArch=\"$(dpkg --print-architecture)\" && curl -fsSLO --compressed \"https://nodejs.org/dist/v24.4.0/node-v24.4.0-linux-x64.tar.xz\" && tar -xJf \"node-v24.4.0-linux-x64.tar.xz\" -C /usr/local --strip-components=1 --no-same-owner && ln -s /usr/local/bin/node /usr/local/bin/nodejs # buildkit",
      "instructionKnown": true,
      "owner": "shared"
    },
    {
      "index": 3,
      "diffId": "sha256:5786d8dda09862b1750f9a1defc4ffc9c5d44cec24d5d9b469d18eb4b3e35aeb",
      "chainId": "sha256:062a4d976280ba977ad7ab4c66f2f3ada6e97375b12509fb84e0a1386508d631",
      "compressedDigest": "sha256:b5b9355410f3eb9a9adc2585803e3f30669c6482bfad33faa0a88022f4e2febb",
      "compressedSize": 1024,
      "contentBytes": 673066,
      "entryCount": 7,
      "instruction": "RUN set -ex && curl -fsSLO --compressed \"https://yarnpkg.com/downloads/1.22.22/yarn-v1.22.22.tar.gz\" && mkdir -p /opt && tar -xzf yarn-v1.22.22.tar.gz -C /opt/ && ln -s /opt/yarn-v1.22.22/bin/yarn /usr/local/bin/yarn",
      "instructionRaw": "RUN /bin/sh -c set -ex && curl -fsSLO --compressed \"https://yarnpkg.com/downloads/1.22.22/yarn-v1.22.22.tar.gz\" && mkdir -p /opt && tar -xzf yarn-v1.22.22.tar.gz -C /opt/ && ln -s /opt/yarn-v1.22.22/bin/yarn /usr/local/bin/yarn # buildkit",
      "instructionKnown": true,
      "owner": "shared"
    },
    {
      "index": 4,
      "diffId": "sha256:51dbafdf323135194b352656c4447c07c943062f660a5467aed0c1e95055cfd3",
      "chainId": "sha256:e0d0cfb29fc442513e19626cb594bd0a7494fd31ea67b44a76bf78672b34e874",
      "compressedDigest": "sha256:1c560b85cab0e34c39210bee260dec9460a50bf7ff67190d9bd88d7b7608b0bc",
      "compressedSize": 90,
      "contentBytes": 0,
      "entryCount": 1,
      "instruction": "WORKDIR /app",
      "instructionRaw": "WORKDIR /app",
      "instructionKnown": true,
      "owner": "shared"
    }
  ],
  "leftBranch": [
    {
      "index": 5,
      "diffId": "sha256:e9e90078b28fc3cdf9066fd5aed8e9d84e679e4021710bab3514b13edde45a38",
      "chainId": "sha256:632ddd71d81b0769537806a1cc9691809ec0b788612840a7b429fe28f7ee4380",
      "compressedDigest": "sha256:356bf47969ac857378924de889afc6b5186895ef0e784b2edaf3660509cbf8d7",
      "compressedSize": 1000,
      "contentBytes": 231575,
      "entryCount": 18,
      "instruction": "COPY . .",
      "instructionRaw": "COPY . . # buildkit",
      "instructionKnown": true,
      "owner": "left"
    },
    {
      "index": 6,
      "diffId": "sha256:e1e5a92bce27397e1139cab55681145ae2e967f06ae37e592cf381ee07edadd5",
      "chainId": "sha256:e5e7f343b48336acce19571a964293f2cb16b15f363e4c8ec5b1a3ce9d387c31",
      "compressedDigest": "sha256:984a20ed8744ec5604dc2db4da17bbb6694944a7a91af135b92a742ddc2c3ac8",
      "compressedSize": 19755,
      "contentBytes": 8741966,
      "entryCount": 309,
      "instruction": "RUN npm install",
      "instructionRaw": "RUN /bin/sh -c npm install # buildkit",
      "instructionKnown": true,
      "owner": "left"
    },
    {
      "index": 7,
      "diffId": "sha256:53375aa0d56b9b8cd7a275efd27bbc383da8e2c0905cabc0d77c9cdcd109ad2f",
      "chainId": "sha256:dab59e0f096817c4cd4ab10a2d9814c5b866a5df02ad9aa4bfbb9770ea5ab11c",
      "compressedDigest": "sha256:436cf902f1e8138325d8452ba08c58c85652981cf04eaa9da4604e3dea392c14",
      "compressedSize": 17700,
      "contentBytes": 13161201,
      "entryCount": 56,
      "instruction": "RUN apt-get update -y && apt-get install -y ffmpeg && rm -rf /var/lib/{apt,dpkg,cache,log}/",
      "instructionRaw": "RUN /bin/sh -c apt-get update -y && apt-get install -y ffmpeg && rm -rf /var/lib/{apt,dpkg,cache,log}/ # buildkit",
      "instructionKnown": true,
      "owner": "left"
    }
  ],
  "rightBranch": [
    {
      "index": 5,
      "diffId": "sha256:a95f925f1da3cd1909dd6bf304d3f9d51fbc88e0731a82e3043f62d2697e6fed",
      "chainId": "sha256:90e7d3ada115f57b58a809be62c1a34107f3beaea94a1e0c16f159d60e42406e",
      "compressedDigest": "sha256:caf21a228f5e0ac188fb2a22b9fbd8748d2af98fbfcf42bccd3647efee078651",
      "compressedSize": 7858,
      "contentBytes": 5224811,
      "entryCount": 81,
      "instruction": "COPY . .",
      "instructionRaw": "COPY . . # buildkit",
      "instructionKnown": true,
      "owner": "right"
    },
    {
      "index": 6,
      "diffId": "sha256:a5eb42809cc46ca35fde2763169c76b72511c5f054e7d3990375f75cf5511349",
      "chainId": "sha256:82d264ad67a5b6707ac02ff7d953ce0c5c4940af520d6caad5c49f4da1d721e7",
      "compressedDigest": "sha256:006d981f5aeafb12ac54102a8ce5fb6849e0d59ca05c53c2bc835f2c29d82ff8",
      "compressedSize": 19748,
      "contentBytes": 8741966,
      "entryCount": 309,
      "instruction": "RUN npm install",
      "instructionRaw": "RUN /bin/sh -c npm install # buildkit",
      "instructionKnown": true,
      "owner": "right"
    },
    {
      "index": 7,
      "diffId": "sha256:53375aa0d56b9b8cd7a275efd27bbc383da8e2c0905cabc0d77c9cdcd109ad2f",
      "chainId": "sha256:7b004d6b23676f090f8d7a9954093a4f5d5466bb6439e8a9d88adfddb035e935",
      "compressedDigest": "sha256:436cf902f1e8138325d8452ba08c58c85652981cf04eaa9da4604e3dea392c14",
      "compressedSize": 17700,
      "contentBytes": 13161201,
      "entryCount": 56,
      "instruction": "RUN apt-get update -y && apt-get install -y ffmpeg && rm -rf /var/lib/{apt,dpkg,cache,log}/",
      "instructionRaw": "RUN /bin/sh -c apt-get update -y && apt-get install -y ffmpeg && rm -rf /var/lib/{apt,dpkg,cache,log}/ # buildkit",
      "instructionKnown": true,
      "owner": "right"
    }
  ],
  "couldBeShared": [
    {
      "leftIndex": 6,
      "rightIndex": 6,
      "diffIdEqual": false
    },
    {
      "leftIndex": 7,
      "rightIndex": 7,
      "diffIdEqual": true
    }
  ],
  "maxLayerBytes": 18342917
};

export const GOLDEN_IMAGES: ImagesResponse = {
  "images": [
    {
      "id": "sha256:7fe589d5b47ae27cc334934fe24f61268d342dde7bb1c404e7f36fdb11a7626f",
      "refNames": [
        "disjoint:a"
      ],
      "source": "fixture",
      "platform": "linux/amd64",
      "layerCount": 2,
      "totalBytes": 8392635,
      "createdAt": "2026-07-04T10:00:00Z",
      "ingestedAt": "2026-08-29T23:39:15.92985273Z",
      "pinned": true
    },
    {
      "id": "sha256:295069676f3b000f01937a6254270e4c30cfbb5ee91726f008f6098f103d065b",
      "refNames": [
        "disjoint:b"
      ],
      "source": "fixture",
      "platform": "linux/amd64",
      "layerCount": 3,
      "totalBytes": 9650670,
      "createdAt": "2026-07-04T11:00:00Z",
      "ingestedAt": "2026-08-29T23:39:15.949355397Z",
      "pinned": true
    },
    {
      "id": "sha256:132ebb8dc6893eb85ecdac92bed1025991732658af7b3fc05b995dda36717d91",
      "refNames": [
        "edgecase:opaque"
      ],
      "source": "fixture",
      "platform": "linux/amd64",
      "layerCount": 3,
      "totalBytes": 171340,
      "createdAt": "2026-07-20T14:00:00Z",
      "ingestedAt": "2026-08-29T23:39:15.955989397Z",
      "pinned": true
    },
    {
      "id": "sha256:b26e3e3ab3ab088d8ec5b878b4fdc372701e6b429e49a102d8e3dfd6f6c05461",
      "refNames": [
        "edgecase:plain"
      ],
      "source": "fixture",
      "platform": "linux/amd64",
      "layerCount": 3,
      "totalBytes": 170252,
      "createdAt": "2026-07-20T14:00:00Z",
      "ingestedAt": "2026-08-29T23:39:15.960077355Z",
      "pinned": true
    },
    {
      "id": "sha256:7c354f60d198fee7ba6ce2df9cead2a74748de8cb3a1a6049b63727a7f6d3fc8",
      "refNames": [
        "example:v1"
      ],
      "source": "fixture",
      "platform": "linux/amd64",
      "layerCount": 8,
      "totalBytes": 74074241,
      "createdAt": "2026-08-27T14:02:00Z",
      "ingestedAt": "2026-08-29T23:39:16.065663522Z",
      "pinned": true
    },
    {
      "id": "sha256:2b27ab1608ab31a265a7265e982e90aeb558205e8cb4e077fde0543c8bac3c0e",
      "refNames": [
        "example:v2"
      ],
      "source": "fixture",
      "platform": "linux/amd64",
      "layerCount": 8,
      "totalBytes": 79067477,
      "createdAt": "2026-08-29T09:41:00Z",
      "ingestedAt": "2026-08-29T23:39:16.087870397Z",
      "pinned": true
    },
    {
      "id": "sha256:04387f39128dc74eb2d0aba3ce76ca3fc0534973c1c4eee479b5a9c0b604b29c",
      "refNames": [
        "prefix:base"
      ],
      "source": "fixture",
      "platform": "linux/amd64",
      "layerCount": 3,
      "totalBytes": 7313096,
      "createdAt": "2026-07-03T10:00:00Z",
      "ingestedAt": "2026-08-29T23:39:16.101464106Z",
      "pinned": true
    },
    {
      "id": "sha256:2e32e85940cf38625a676325eac8bd28d471afc753a12d8fce6d6cd5621ebbbd",
      "refNames": [
        "prefix:extended"
      ],
      "source": "fixture",
      "platform": "linux/amd64",
      "layerCount": 5,
      "totalBytes": 7316156,
      "createdAt": "2026-07-03T10:04:00Z",
      "ingestedAt": "2026-08-29T23:39:16.105302356Z",
      "pinned": true
    },
    {
      "id": "sha256:7ef087333a887f4480576ec4c5399ecca2519643f9ddc6dc8a56b70dba8fdbf6",
      "refNames": [
        "wide:v1"
      ],
      "source": "fixture",
      "platform": "linux/amd64",
      "layerCount": 1,
      "totalBytes": 515973,
      "createdAt": "2026-07-28T06:30:00Z",
      "ingestedAt": "2026-08-29T23:39:16.120660064Z",
      "pinned": true
    },
    {
      "id": "sha256:96ceea53ec2f993b420620e4671c2fab8c0d0ba63f046ad7088c0a4cd20c189d",
      "refNames": [
        "wide:v2"
      ],
      "source": "fixture",
      "platform": "linux/amd64",
      "layerCount": 2,
      "totalBytes": 517047,
      "createdAt": "2026-07-29T06:30:00Z",
      "ingestedAt": "2026-08-29T23:39:16.122870397Z",
      "pinned": true
    }
  ]
};

/**
 * A real `/diff/tree` root page (depth=2, filter=changed) for
 * `example:v1` @ 7 vs `example:v2` @ 8 — the golden selection. Captured from
 * the running server so the adapter tests assert against the wire shape,
 * including the omitted-when-zero change breakdowns.
 * Regenerate with:
 *   curl -s "localhost:8080/api/v1/diff/tree?left=<v1>&right=<v2>&leftLayers=7&rightLayers=8&filter=changed&depth=2"
 */
export const GOLDEN_TREE_ROOT: TreePage = {
  "path": "/",
  "rows": [
    {
      "name": "app",
      "path": "/app",
      "status": "modified",
      "left": {
        "kind": "dir",
        "mode": 493,
        "sizeBytes": 0
      },
      "right": {
        "kind": "dir",
        "mode": 493,
        "sizeBytes": 0
      },
      "agg": {
        "leftBytes": 8973541,
        "rightBytes": 13966777,
        "leftFiles": 249,
        "rightFiles": 307,
        "addedBytes": 4996672,
        "removedBytes": 4270,
        "modifiedBytesLeft": 7178,
        "modifiedBytesRight": 8012,
        "addedFiles": 61,
        "removedFiles": 3,
        "modifiedFiles": 2
      },
      "hasChildren": true,
      "childCount": 5,
      "children": [
        {
          "name": ".git",
          "path": "/app/.git",
          "status": "added",
          "right": {
            "kind": "dir",
            "mode": 493,
            "sizeBytes": 0
          },
          "agg": {
            "leftBytes": 0,
            "rightBytes": 4834468,
            "leftFiles": 0,
            "rightFiles": 59,
            "addedBytes": 4834468,
            "addedFiles": 59
          },
          "hasChildren": true,
          "childCount": 7
        },
        {
          "name": "src",
          "path": "/app/src",
          "status": "modified",
          "left": {
            "kind": "dir",
            "mode": 493,
            "sizeBytes": 0
          },
          "right": {
            "kind": "dir",
            "mode": 493,
            "sizeBytes": 0
          },
          "agg": {
            "leftBytes": 25119,
            "rightBytes": 20988,
            "leftFiles": 7,
            "rightFiles": 4,
            "removedBytes": 4270,
            "modifiedBytesLeft": 2966,
            "modifiedBytesRight": 3105,
            "removedFiles": 3,
            "modifiedFiles": 1
          },
          "hasChildren": true,
          "childCount": 3
        },
        {
          "name": ".env",
          "path": "/app/.env",
          "status": "added",
          "right": {
            "kind": "file",
            "mode": 384,
            "sizeBytes": 412
          },
          "agg": {
            "leftBytes": 0,
            "rightBytes": 412,
            "leftFiles": 0,
            "rightFiles": 1,
            "addedBytes": 412,
            "addedFiles": 1
          },
          "hasChildren": false,
          "childCount": 0
        },
        {
          "name": "debug.log",
          "path": "/app/debug.log",
          "status": "added",
          "right": {
            "kind": "file",
            "mode": 420,
            "sizeBytes": 161792
          },
          "agg": {
            "leftBytes": 0,
            "rightBytes": 161792,
            "leftFiles": 0,
            "rightFiles": 1,
            "addedBytes": 161792,
            "addedFiles": 1
          },
          "hasChildren": false,
          "childCount": 0
        },
        {
          "name": "main.js",
          "path": "/app/main.js",
          "status": "modified",
          "left": {
            "kind": "file",
            "mode": 420,
            "sizeBytes": 4212
          },
          "right": {
            "kind": "file",
            "mode": 420,
            "sizeBytes": 4907
          },
          "agg": {
            "leftBytes": 4212,
            "rightBytes": 4907,
            "leftFiles": 1,
            "rightFiles": 1,
            "modifiedBytesLeft": 4212,
            "modifiedBytesRight": 4907,
            "modifiedFiles": 1
          },
          "hasChildren": false,
          "childCount": 0
        }
      ]
    },
    {
      "name": "etc",
      "path": "/etc",
      "status": "modified",
      "left": {
        "kind": "dir",
        "mode": 493,
        "sizeBytes": 0
      },
      "right": {
        "kind": "dir",
        "mode": 493,
        "sizeBytes": 0
      },
      "agg": {
        "leftBytes": 398757,
        "rightBytes": 398799,
        "leftFiles": 69,
        "rightFiles": 70,
        "addedBytes": 42,
        "addedFiles": 1
      },
      "hasChildren": true,
      "childCount": 1,
      "children": [
        {
          "name": "OpenCL",
          "path": "/etc/OpenCL",
          "status": "added",
          "right": {
            "kind": "dir",
            "mode": 493,
            "sizeBytes": 0
          },
          "agg": {
            "leftBytes": 0,
            "rightBytes": 42,
            "leftFiles": 0,
            "rightFiles": 1,
            "addedBytes": 42,
            "addedFiles": 1
          },
          "hasChildren": true,
          "childCount": 1
        }
      ]
    },
    {
      "name": "usr",
      "path": "/usr",
      "status": "modified",
      "left": {
        "kind": "dir",
        "mode": 493,
        "sizeBytes": 0
      },
      "right": {
        "kind": "dir",
        "mode": 493,
        "sizeBytes": 0
      },
      "agg": {
        "leftBytes": 48274110,
        "rightBytes": 61435269,
        "leftFiles": 281,
        "rightFiles": 329,
        "addedBytes": 13161159,
        "addedFiles": 48
      },
      "hasChildren": true,
      "childCount": 3,
      "children": [
        {
          "name": "bin",
          "path": "/usr/bin",
          "status": "modified",
          "left": {
            "kind": "dir",
            "mode": 493,
            "sizeBytes": 0
          },
          "right": {
            "kind": "dir",
            "mode": 493,
            "sizeBytes": 0
          },
          "agg": {
            "leftBytes": 1954236,
            "rightBytes": 2605500,
            "leftFiles": 20,
            "rightFiles": 23,
            "addedBytes": 651264,
            "addedFiles": 3
          },
          "hasChildren": true,
          "childCount": 3
        },
        {
          "name": "lib",
          "path": "/usr/lib",
          "status": "modified",
          "left": {
            "kind": "dir",
            "mode": 493,
            "sizeBytes": 0
          },
          "right": {
            "kind": "dir",
            "mode": 493,
            "sizeBytes": 0
          },
          "agg": {
            "leftBytes": 23384971,
            "rightBytes": 35882557,
            "leftFiles": 68,
            "rightFiles": 112,
            "addedBytes": 12497586,
            "addedFiles": 44
          },
          "hasChildren": true,
          "childCount": 1
        },
        {
          "name": "share",
          "path": "/usr/share",
          "status": "modified",
          "left": {
            "kind": "dir",
            "mode": 493,
            "sizeBytes": 0
          },
          "right": {
            "kind": "dir",
            "mode": 493,
            "sizeBytes": 0
          },
          "agg": {
            "leftBytes": 6641639,
            "rightBytes": 6653948,
            "leftFiles": 64,
            "rightFiles": 65,
            "addedBytes": 12309,
            "addedFiles": 1
          },
          "hasChildren": true,
          "childCount": 1
        }
      ]
    },
    {
      "name": "var",
      "path": "/var",
      "status": "modified",
      "left": {
        "kind": "dir",
        "mode": 493,
        "sizeBytes": 0
      },
      "right": {
        "kind": "dir",
        "mode": 493,
        "sizeBytes": 0
      },
      "agg": {
        "leftBytes": 1741598,
        "rightBytes": 0,
        "leftFiles": 31,
        "rightFiles": 0,
        "removedBytes": 1741598,
        "removedFiles": 31
      },
      "hasChildren": true,
      "childCount": 1,
      "children": [
        {
          "name": "lib",
          "path": "/var/lib",
          "status": "modified",
          "left": {
            "kind": "dir",
            "mode": 493,
            "sizeBytes": 0
          },
          "right": {
            "kind": "dir",
            "mode": 493,
            "sizeBytes": 0
          },
          "agg": {
            "leftBytes": 1741598,
            "rightBytes": 0,
            "leftFiles": 31,
            "rightFiles": 0,
            "removedBytes": 1741598,
            "removedFiles": 31
          },
          "hasChildren": true,
          "childCount": 4
        }
      ]
    }
  ],
  "totalRows": 4,
  "maxSiblingBytes": 109709379,
  "pathStatus": "modified",
  "pathAgg": {
    "leftBytes": 60913040,
    "rightBytes": 77325879,
    "leftFiles": 648,
    "rightFiles": 724,
    "addedBytes": 18157873,
    "removedBytes": 1745868,
    "modifiedBytesLeft": 7178,
    "modifiedBytesRight": 8012,
    "addedFiles": 110,
    "removedFiles": 34,
    "modifiedFiles": 2
  }
};

/** The same comparison's `/app` listing, depth 1. */
export const GOLDEN_TREE_APP: TreePage = {
  "path": "/app",
  "rows": [
    {
      "name": ".git",
      "path": "/app/.git",
      "status": "added",
      "right": {
        "kind": "dir",
        "mode": 493,
        "sizeBytes": 0
      },
      "agg": {
        "leftBytes": 0,
        "rightBytes": 4834468,
        "leftFiles": 0,
        "rightFiles": 59,
        "addedBytes": 4834468,
        "addedFiles": 59
      },
      "hasChildren": true,
      "childCount": 7
    },
    {
      "name": "src",
      "path": "/app/src",
      "status": "modified",
      "left": {
        "kind": "dir",
        "mode": 493,
        "sizeBytes": 0
      },
      "right": {
        "kind": "dir",
        "mode": 493,
        "sizeBytes": 0
      },
      "agg": {
        "leftBytes": 25119,
        "rightBytes": 20988,
        "leftFiles": 7,
        "rightFiles": 4,
        "removedBytes": 4270,
        "modifiedBytesLeft": 2966,
        "modifiedBytesRight": 3105,
        "removedFiles": 3,
        "modifiedFiles": 1
      },
      "hasChildren": true,
      "childCount": 3
    },
    {
      "name": ".env",
      "path": "/app/.env",
      "status": "added",
      "right": {
        "kind": "file",
        "mode": 384,
        "sizeBytes": 412
      },
      "agg": {
        "leftBytes": 0,
        "rightBytes": 412,
        "leftFiles": 0,
        "rightFiles": 1,
        "addedBytes": 412,
        "addedFiles": 1
      },
      "hasChildren": false,
      "childCount": 0
    },
    {
      "name": "debug.log",
      "path": "/app/debug.log",
      "status": "added",
      "right": {
        "kind": "file",
        "mode": 420,
        "sizeBytes": 161792
      },
      "agg": {
        "leftBytes": 0,
        "rightBytes": 161792,
        "leftFiles": 0,
        "rightFiles": 1,
        "addedBytes": 161792,
        "addedFiles": 1
      },
      "hasChildren": false,
      "childCount": 0
    },
    {
      "name": "main.js",
      "path": "/app/main.js",
      "status": "modified",
      "left": {
        "kind": "file",
        "mode": 420,
        "sizeBytes": 4212
      },
      "right": {
        "kind": "file",
        "mode": 420,
        "sizeBytes": 4907
      },
      "agg": {
        "leftBytes": 4212,
        "rightBytes": 4907,
        "leftFiles": 1,
        "rightFiles": 1,
        "modifiedBytesLeft": 4212,
        "modifiedBytesRight": 4907,
        "modifiedFiles": 1
      },
      "hasChildren": false,
      "childCount": 0
    }
  ],
  "totalRows": 5,
  "maxSiblingBytes": 4834468,
  "pathStatus": "modified",
  "pathAgg": {
    "leftBytes": 8973541,
    "rightBytes": 13966777,
    "leftFiles": 249,
    "rightFiles": 307,
    "addedBytes": 4996672,
    "removedBytes": 4270,
    "modifiedBytesLeft": 7178,
    "modifiedBytesRight": 8012,
    "addedFiles": 61,
    "removedFiles": 3,
    "modifiedFiles": 2
  }
};
