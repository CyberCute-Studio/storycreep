// How far a pointer moves before its press is a drag rather than a tap (PRD
// NFR-TOUCH-05), which every drag of the app counts from: the project's
// tree's move of a child by its handle, and the canvas's pan and rubber
// band (canvas/gestures.ts), which a mouse, more precise than a finger,
// starts sooner.

/** How far, in CSS pixels, a finger moves before its press is a drag, so that a tap is not misread as one (NFR-TOUCH-05). */
export const dragThreshold = 8;
