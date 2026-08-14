// Package issueview fetches one td issue and renders it as a reusable card.
//
// Hosts size the card and decide when it is active. An active card walks
// parent/child/sibling issues with the arrow keys; an inactive one only
// scrolls, so a modal can share those keys with its own chrome until the
// user tabs to the card and presses enter, or clicks it.
package issueview
