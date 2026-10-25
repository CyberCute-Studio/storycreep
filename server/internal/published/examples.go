package published

// The schemas' examples, which describe one project, Harbour, the PRD's
// (§6.5, §8.5): the examples that the PRD gives, each of which a test finds
// among these, and the others that the files need. Their expressions are what
// expr.Compile gives of their sources against the project that the examples
// declare, their texts what richtext derives of the rich text's own examples,
// and their hashes stand for the files' and the content's.

// manifestExamples are a release's manifest, the PRD's, and a draft's.
var manifestExamples = []string{
	`{
  "astVersion": 1,
  "boards": [
    { "entryNodeId": "nd_01HQ8Z41M9D2PK7VW3RB6XGHTY", "file": "boards/harbour-intro.json", "id": "bd_01HQ8Z3XK4F7NAWR5TCQ9M2BVD", "name": "Harbour Intro", "slug": "harbour-intro" },
    { "entryNodeId": "nd_01HQ8Z9R2M5K8W3XTVNPQ7DCBA", "file": "boards/elara-first-meeting.json", "id": "bd_01HQ8Z9N4T7K2W5XRVNPQ8DCBE", "name": "Elara — First Meeting", "slug": "elara-first-meeting" },
    { "entryNodeId": "nd_01HQ8ZB1T4V7K2MC9XRPW5NDYF", "file": "boards/harbourmaster.json", "id": "bd_01HQ8ZAX6P3R9T1K7MWVND2CQJ", "name": "Harbourmaster", "slug": "harbourmaster" }
  ],
  "counts": { "boards": 3, "components": 4, "connections": 58, "nodes": 42, "variables": 6 },
  "entry": { "boardId": "bd_01HQ8Z3XK4F7NAWR5TCQ9M2BVD", "nodeId": "nd_01HQ8Z41M9D2PK7VW3RB6XGHTY" },
  "files": {
    "boards/elara-first-meeting.json": "sha256:7b1e84cc413fe273e190ecee734b4c6021588589e8445ce6b7575406f5a11671",
    "boards/harbour-intro.json": "sha256:0ac9d71fa2c808777b7b27a17e20f7940d7ffa2f67251f9ef86d32e6b6a8ca1c",
    "boards/harbourmaster.json": "sha256:31f4a75bae9af1993eed5e17762ae6f8469f874fc5352dc61bd5c3465dcfd141",
    "project/component_types.json": "sha256:a9021a2cc974b32d40c775d432206eaf8502d205b05cee1d2035d68e711c09e0",
    "project/components.json": "sha256:c7710aa17083079d59b058cb5cc1bdd9d151edf60ab244f6b599126917401517",
    "project/settings.json": "sha256:4e0d5d8cdcc789e42b0ec2f60affb1940e2a76385581b8b468734f9c3a1d660b",
    "project/variables.json": "sha256:9d3acc5d88a51aca0a50d5cc3dc65c0ee46853e8f5dc44c8e20eb779d31747f9"
  },
  "generator": { "name": "storycreep", "version": "0.9.0" },
  "project": { "id": "prj_01HQ8Z2YV0C4E8M1T6RN5KQJWP", "name": "Harbour", "slug": "harbour" },
  "schemaVersion": 1,
  "snapshot": {
    "channel": "release",
    "contentHash": "sha256:5f2ced7002b439e9ac845f22357d822bac1444730fbdb6016d3ec9432297b9ec",
    "id": "snp_01HQ9A7TBE5N2WQX8K4YJ3MDCR",
    "note": "Harbour intro + keeper gate",
    "publishedAt": "2026-10-12T18:04:31Z",
    "version": "1.4.0"
  }
}`,
	`{
  "astVersion": 1,
  "boards": [
    { "file": "boards/harbour-intro.json", "id": "bd_01HQ8Z3XK4F7NAWR5TCQ9M2BVD", "name": "Harbour Intro", "slug": "harbour-intro" }
  ],
  "counts": { "boards": 1, "components": 0, "connections": 0, "nodes": 0, "variables": 0 },
  "files": {
    "boards/harbour-intro.json": "sha256:0ac9d71fa2c808777b7b27a17e20f7940d7ffa2f67251f9ef86d32e6b6a8ca1c",
    "project/component_types.json": "sha256:a9021a2cc974b32d40c775d432206eaf8502d205b05cee1d2035d68e711c09e0",
    "project/components.json": "sha256:c7710aa17083079d59b058cb5cc1bdd9d151edf60ab244f6b599126917401517",
    "project/settings.json": "sha256:4e0d5d8cdcc789e42b0ec2f60affb1940e2a76385581b8b468734f9c3a1d660b",
    "project/variables.json": "sha256:9d3acc5d88a51aca0a50d5cc3dc65c0ee46853e8f5dc44c8e20eb779d31747f9"
  },
  "generator": { "name": "storycreep", "version": "0.9.0" },
  "project": { "id": "prj_01HQ8Z2YV0C4E8M1T6RN5KQJWP", "name": "Harbour", "slug": "harbour" },
  "schemaVersion": 1,
  "snapshot": {
    "channel": "draft",
    "contentHash": "sha256:e18b7743ce348d9284d677a185f33295b92266cc435a5b5f775029b300066d26",
    "pulledAt": "2026-10-13T09:15:00Z"
  }
}`,
}

// settingsExamples are a project's settings.
var settingsExamples = []string{
	`{
  "entry": { "boardId": "bd_01HQ8Z3XK4F7NAWR5TCQ9M2BVD", "nodeId": "nd_01HQ8Z41M9D2PK7VW3RB6XGHTY" },
  "schemaVersion": 1,
  "signals": ["lighthouse_lit"]
}`,
}

// variablesExamples are the PRD's global variables, and a variable of each
// other type, of a board.
var variablesExamples = []string{
	`{
  "schemaVersion": 1,
  "variables": [
    {
      "default": false,
      "description": "Player has met Elara at the lighthouse.",
      "id": "var_01HQ8Z5BRT9C4F2M7XKDPV6NHA",
      "key": "met_elara",
      "scope": "global",
      "type": "bool"
    },
    {
      "default": 0,
      "id": "var_01HQ8Z5C2K8W1D5J9NTQZR4YBE",
      "key": "harbour_rumours",
      "scope": "global",
      "type": "int"
    }
  ]
}`,
	`{
  "schemaVersion": 1,
  "variables": [
    {
      "boardId": "bd_01HQ8Z9N4T7K2W5XRVNPQ8DCBE",
      "default": 0.5,
      "description": "How far the lamp is turned up while Elara talks.",
      "id": "var_01HQ8Z5D3M9X2E6K0PVRT5ZCGF",
      "key": "lamp",
      "resetOnEnter": true,
      "scope": "board",
      "type": "float"
    }
  ]
}`,
}

// componentTypesExamples are a project's component types.
var componentTypesExamples = []string{
	`{
  "componentTypes": [
    {
      "attributes": [
        { "default": 0, "id": "atr_01HQ8Z8M4K7R2W9XTVNPQ5DCBF", "key": "trust", "name": "Trust", "runtime": true, "type": "int" },
        { "default": "calm", "id": "atr_01HQ8Z8N5K2R7W4XTVNPQ6DCBG", "key": "mood", "name": "Mood", "options": ["calm", "wary", "angry"], "runtime": true, "type": "enum" },
        { "default": "", "id": "atr_01HQ8Z8P6M3R8W5XTVNPQ7DCBH", "key": "title", "name": "Title", "runtime": false, "type": "string" },
        { "default": "", "id": "atr_01HQ8Z8Q7N4R9W6XTVNPQ8DCBJ", "key": "bio", "name": "Biography", "runtime": false, "type": "text" }
      ],
      "id": "ct_01HQ8Z6T2M5K9W3XRVNPQ7DCBA",
      "key": "character",
      "name": "Character"
    },
    {
      "attributes": [],
      "id": "ct_01HQ8Z6V3N6K0W4XRVNPQ8DCBB",
      "key": "item",
      "name": "Item"
    }
  ],
  "schemaVersion": 1
}`,
}

// componentsExamples are a project's components.
var componentsExamples = []string{
	`{
  "components": [
    {
      "attributes": {
        "atr_01HQ8Z8M4K7R2W9XTVNPQ5DCBF": 1,
        "atr_01HQ8Z8N5K2R7W4XTVNPQ6DCBG": "wary",
        "atr_01HQ8Z8P6M3R8W5XTVNPQ7DCBH": "Keeper of the Lighthouse",
        "atr_01HQ8Z8Q7N4R9W6XTVNPQ8DCBJ": "Raised on the rock.\nKnows every ship by its lamp."
      },
      "componentTypeId": "ct_01HQ8Z6T2M5K9W3XRVNPQ7DCBA",
      "id": "cmp_01HQ8Z7KD3M9P2W5XRTVNQ4BCF",
      "image": "ast_01HQ8ZG2K5M8W1R4XTVNPQ6DCB",
      "key": "elara",
      "name": "Elara"
    },
    {
      "attributes": {},
      "componentTypeId": "ct_01HQ8Z6V3N6K0W4XRVNPQ8DCBB",
      "id": "cmp_01HQ8Z7M5E4N1Q3W6YSVPR5CDG",
      "key": "brass_key",
      "name": "Brass Key"
    }
  ],
  "schemaVersion": 1
}`,
}

// boardExamples are the PRD's harbourmaster board, abridged, and Elara's
// first meeting, with the PRD's jumper.
var boardExamples = []string{
	`{
  "board": {
    "entryNodeId": "nd_01HQ8ZB1T4V7K2MC9XRPW5NDYF",
    "id": "bd_01HQ8ZAX6P3R9T1K7MWVND2CQJ",
    "name": "Harbourmaster",
    "slug": "harbourmaster"
  },
  "connections": [
    {
      "condition": {
        "ast": {
          "kind": "binary",
          "left": { "kind": "var", "name": "met_elara", "ref": "var_01HQ8Z5BRT9C4F2M7XKDPV6NHA" },
          "op": "eq",
          "right": { "kind": "lit", "type": "bool", "value": true }
        },
        "source": "met_elara == true"
      },
      "from": "nd_01HQ8ZB1T4V7K2MC9XRPW5NDYF",
      "fromPort": "out",
      "id": "cn_01HQ8ZC4M7D1F8K3NRTQW9XJVB",
      "label": { "segments": [{ "kind": "text", "marks": [], "value": "The keeper sent me." }] },
      "onTraverse": {
        "ast": {
          "kind": "script",
          "statements": [
            {
              "kind": "assign",
              "op": "+=",
              "target": { "kind": "var", "name": "harbour_rumours", "ref": "var_01HQ8Z5C2K8W1D5J9NTQZR4YBE" },
              "value": { "kind": "lit", "type": "int", "value": 1 }
            }
          ]
        },
        "source": "harbour_rumours += 1"
      },
      "order": 0,
      "to": "nd_01HQ8ZB9W2K5R8T4MCXVND7QPJ"
    }
  ],
  "nodes": [
    {
      "id": "nd_01HQ8ZB1T4V7K2MC9XRPW5NDYF",
      "isEnding": false,
      "kind": "dialogue",
      "position": { "x": 240, "y": 120 },
      "speaker": "cmp_01HQ8Z7KD3M9P2W5XRTVNQ4BCF",
      "text": {
        "preview": "Harbour's closed to strangers. State your business.",
        "segments": [
          { "kind": "text", "marks": [], "value": "Harbour's closed to strangers. State your business." }
        ]
      },
      "title": "Harbourmaster greets you",
      "type": "element"
    },
    {
      "id": "nd_01HQ8ZD2P5K8W1M4RTXVNQ9CBJ",
      "outputs": [
        {
          "condition": {
            "ast": {
              "kind": "binary",
              "left": { "attribute": "atr_01HQ8Z8M4K7R2W9XTVNPQ5DCBF", "kind": "attr", "name": "elara.trust", "ref": "cmp_01HQ8Z7KD3M9P2W5XRTVNQ4BCF" },
              "op": "gte",
              "right": { "kind": "lit", "type": "int", "value": 3 }
            },
            "source": "elara.trust >= 3"
          },
          "id": "bo_01HQ8ZE5R8K2M7W4XTVNPQ3DCB",
          "kind": "if",
          "order": 0
        },
        { "id": "bo_01HQ8ZE6T9M3K8W2XRVNPQ4DCB", "kind": "else", "order": 1 }
      ],
      "position": { "x": 640, "y": 120 },
      "title": "Does he trust the keeper?",
      "type": "branch"
    }
  ],
  "schemaVersion": 1
}`,
	`{
  "board": {
    "entryNodeId": "nd_01HQ8Z9R2M5K8W3XTVNPQ7DCBA",
    "id": "bd_01HQ8Z9N4T7K2W5XRVNPQ8DCBE",
    "name": "Elara — First Meeting",
    "slug": "elara-first-meeting"
  },
  "connections": [
    {
      "from": "nd_01HQ8Z9R2M5K8W3XTVNPQ7DCBA",
      "fromPort": "out",
      "id": "cn_01HQ8ZH3M6K9W2R5XTVNPQ7DCB",
      "label": { "segments": [{ "kind": "text", "marks": [], "value": "Ask about the dark light." }] },
      "order": 0,
      "to": "nd_01HQ8ZF7K2M9W4R8XTVNPQ5DCB"
    }
  ],
  "nodes": [
    {
      "cover": "ast_01HQ8ZG3M6N9W2R5XTVNPQ7DCB",
      "id": "nd_01HQ8Z9R2M5K8W3XTVNPQ7DCBA",
      "isEnding": false,
      "kind": "dialogue",
      "onEnter": {
        "ast": {
          "kind": "script",
          "statements": [
            {
              "kind": "assign",
              "op": "=",
              "target": { "kind": "var", "name": "met_elara", "ref": "var_01HQ8Z5BRT9C4F2M7XKDPV6NHA" },
              "value": { "kind": "lit", "type": "bool", "value": true }
            }
          ]
        },
        "source": "met_elara = true"
      },
      "onExit": {
        "ast": {
          "kind": "script",
          "statements": [
            { "args": [{ "kind": "lit", "type": "string", "value": "lighthouse_lit" }], "function": "signal", "kind": "call" }
          ]
        },
        "source": "signal(\"lighthouse_lit\")"
      },
      "position": { "x": 120.5, "y": -40 },
      "speaker": "cmp_01HQ8Z7KD3M9P2W5XRTVNQ4BCF",
      "tags": ["lighthouse", "Quest"],
      "text": {
        "preview": "Elara has heard {harbour_rumours} rumours. Trust: {elara.trust}",
        "segments": [
          { "kind": "mention", "marks": [], "ref": "cmp_01HQ8Z7KD3M9P2W5XRTVNQ4BCF", "value": "Elara" },
          { "kind": "text", "marks": [], "value": " has heard " },
          { "kind": "value", "marks": [], "name": "harbour_rumours", "ref": "var_01HQ8Z5C2K8W1D5J9NTQZR4YBE" },
          { "kind": "text", "marks": [], "value": " rumours." },
          { "kind": "break" },
          { "kind": "text", "marks": [], "value": "Trust: " },
          { "attribute": "atr_01HQ8Z8M4K7R2W9XTVNPQ5DCBF", "decimals": 1, "kind": "value", "marks": ["bold"], "name": "elara.trust", "ref": "cmp_01HQ8Z7KD3M9P2W5XRTVNQ4BCF" }
        ]
      },
      "title": "Elara at the lighthouse",
      "type": "element"
    },
    {
      "id": "nd_01HQ8ZF7K2M9W4R8XTVNPQ5DCB",
      "position": { "x": 980, "y": 320 },
      "target": { "boardId": "bd_01HQ8ZAX6P3R9T1K7MWVND2CQJ", "nodeId": "nd_01HQ8ZB9W2K5R8T4MCXVND7QPJ" },
      "title": "→ Quest: The Dark Light",
      "type": "jumper"
    }
  ],
  "schemaVersion": 1
}`,
}

// astExamples are the ASTs of the boards' expressions.
var astExamples = []string{
	`{"kind":"binary","left":{"kind":"var","name":"met_elara","ref":"var_01HQ8Z5BRT9C4F2M7XKDPV6NHA"},"op":"eq","right":{"kind":"lit","type":"bool","value":true}}`,
	`{"kind":"binary","left":{"attribute":"atr_01HQ8Z8M4K7R2W9XTVNPQ5DCBF","kind":"attr","name":"elara.trust","ref":"cmp_01HQ8Z7KD3M9P2W5XRTVNQ4BCF"},"op":"gte","right":{"kind":"lit","type":"int","value":3}}`,
	`{"kind":"script","statements":[{"kind":"assign","op":"+=","target":{"kind":"var","name":"harbour_rumours","ref":"var_01HQ8Z5C2K8W1D5J9NTQZR4YBE"},"value":{"kind":"lit","type":"int","value":1}}]}`,
	`{"kind":"script","statements":[{"kind":"assign","op":"=","target":{"kind":"var","name":"met_elara","ref":"var_01HQ8Z5BRT9C4F2M7XKDPV6NHA"},"value":{"kind":"lit","type":"bool","value":true}}]}`,
	`{"kind":"script","statements":[{"args":[{"kind":"lit","type":"string","value":"lighthouse_lit"}],"function":"signal","kind":"call"}]}`,
}

// saveStateExamples are the PRD's save state, and a draft's, as a game that
// read it back with Godot's JSON parser writes it, every number a float.
var saveStateExamples = []string{
	`{
  "stateVersion": 1,
  "projectId": "prj_01HQ8Z2YV0C4E8M1T6RN5KQJWP",
  "snapshotId": "snp_01HQ9A7TBE5N2WQX8K4YJ3MDCR",
  "snapshotVersion": "1.4.0",
  "schemaVersion": 1,
  "cursor": { "boardId": "bd_01HQ8ZAX6P3R9T1K7MWVND2CQJ", "nodeId": "nd_01HQ8ZB1T4V7K2MC9XRPW5NDYF", "phase": "awaiting_choice" },
  "variables": { "var_01HQ8Z5BRT9C4F2M7XKDPV6NHA": true, "var_01HQ8Z5C2K8W1D5J9NTQZR4YBE": 2 },
  "attributes": { "cmp_01HQ8Z7KD3M9P2W5XRTVNQ4BCF": { "atr_01HQ8Z8M4K7R2W9XTVNPQ5DCBF": 3 } },
  "visits": { "nd_01HQ8Z41M9D2PK7VW3RB6XGHTY": 1, "nd_01HQ8ZB1T4V7K2MC9XRPW5NDYF": 2 }
}`,
	`{
  "attributes": { "cmp_01HQ8Z7KD3M9P2W5XRTVNQ4BCF": { "atr_01HQ8Z8M4K7R2W9XTVNPQ5DCBF": 4.0, "atr_01HQ8Z8N5K2R7W4XTVNPQ6DCBG": "calm" } },
  "cursor": { "boardId": "bd_01HQ8Z9N4T7K2W5XRVNPQ8DCBE", "nodeId": "nd_01HQ8Z9R2M5K8W3XTVNPQ7DCBA", "phase": "ended" },
  "projectId": "prj_01HQ8Z2YV0C4E8M1T6RN5KQJWP",
  "schemaVersion": 1.0,
  "stateVersion": 1.0,
  "variables": { "var_01HQ8Z5BRT9C4F2M7XKDPV6NHA": true, "var_01HQ8Z5C2K8W1D5J9NTQZR4YBE": 3.0, "var_01HQ8Z5D3M9X2E6K0PVRT5ZCGF": 0.5 },
  "visits": { "nd_01HQ8Z9R2M5K8W3XTVNPQ7DCBA": 2.0 }
}`,
}
