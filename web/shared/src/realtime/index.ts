// The messages of the real-time channel, the WebSocket of GET /api/v1/ws
// (PRD FR-RT-01), whose types are generated from their JSON Schema,
// schema/realtime/v1/messages.schema.json, so that the client describes no
// message by hand (FR-API-02 AC2).

export * from "./v1.gen";
