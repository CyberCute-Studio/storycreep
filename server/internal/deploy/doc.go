// Package deploy tests the deployment files in the repository's deploy
// directory: the container image that deploy/Dockerfile builds, and the
// compose files (PRD FR-ADMIN-01 AC4, FR-ADMIN-07), and that CI tests with
// the Go that the image is built with. It has only tests, and the binary does
// not use it.
//
// The tests of the compose files' contents run everywhere. The tests that
// need Docker, and an image built by CI, run only when the environment names
// what to test:
//
//   - STORYCREEP_TEST_IMAGE_ARCHIVE, the path of the image for both
//     platforms as an OCI archive, which docker buildx build writes with
//     --output type=oci,dest=<path>;
//   - STORYCREEP_TEST_IMAGE, the name of the image for this machine's
//     platform, loaded into Docker, which docker-compose.yml is started
//     with.
package deploy
