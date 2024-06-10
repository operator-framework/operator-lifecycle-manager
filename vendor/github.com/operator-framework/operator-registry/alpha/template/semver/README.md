## Semver Template:

Since a `catalog template` is identified as an input schema which may be processed to generate a valid FBC, we can define a `semver template` as a schema which uses channel conventions to facilitate the auto-generation of channels along `semver` delimiters.  

[**DISCLAIMER:** since version build metadata [MUST be ignored when determining version precedence](https://semver.org) when using semver, rendering the template will result in an error if two bundles differ only by the build metadata.]

### Schema Goals
The `semver template` must have:
- terse grammar to minimize creation/maintenance effort
- deterministic output
- simple channel promotion for maturing bundles
- demonstration of a common type of channel maturity model
- minor-version (Y-stream), major-version (X-stream) versioning optionality

The resulting FBC must clearly indicate how generated channels relate to template entities

### Schema Anatomy
For convenience and simplicity, this template currently supports hard-coded channel names `Candidate`, `Fast`, and `Stable`, in order of increasing channel stability.  We leverage this relationship to calculate the default channel for the package. 

`GenerateMajorChannels` and `GenerateMinorChannels` dictate whether this template will generate X-stream or Y-stream channels (attributes can be set independently).  If omitted, only minor (Y-stream) channels will be generated.  

Under each channel are a list of bundle image references which contribute to that channel.  

With the following (hypothetical) example we define a mock bundle which has 11 versions, represented across each of the channel types:
```yaml
Schema: olm.semver
GenerateMajorChannels: true
GenerateMinorChannels: true
Candidate:
  Bundles:
  - Image: quay.io/foo/olm:testoperator.v0.1.0
  - Image: quay.io/foo/olm:testoperator.v0.1.1
  - Image: quay.io/foo/olm:testoperator.v0.1.2
  - Image: quay.io/foo/olm:testoperator.v0.1.3
  - Image: quay.io/foo/olm:testoperator.v0.2.0
  - Image: quay.io/foo/olm:testoperator.v0.2.1
  - Image: quay.io/foo/olm:testoperator.v0.2.2
  - Image: quay.io/foo/olm:testoperator.v0.3.0
  - Image: quay.io/foo/olm:testoperator.v1.0.0
  - Image: quay.io/foo/olm:testoperator.v1.0.1
  - Image: quay.io/foo/olm:testoperator.v1.1.0
Fast:
  Bundles:
  - Image: quay.io/foo/olm:testoperator.v0.2.1
  - Image: quay.io/foo/olm:testoperator.v0.2.2
  - Image: quay.io/foo/olm:testoperator.v0.3.0
  - Image: quay.io/foo/olm:testoperator.v1.0.1
  - Image: quay.io/foo/olm:testoperator.v1.1.0
Stable:
  Bundles:
  - Image: quay.io/foo/olm:testoperator.v1.0.1
```
In this example, `Candidate` has the entire version range of bundles,  `Fast` has a mix of older and more-recent versions, and `Stable` channel only has a single published entry. 

### CLI Tool Usage
```
% ./bin/opm alpha render-template semver -h
Generate a file-based catalog from a single 'semver template' file
When FILE is '-' or not provided, the template is read from standard input

Usage:
  opm alpha render-template semver [FILE] [flags]

Flags:
  -h, --help            help for semver
  -o, --output string   Output format (json|yaml|mermaid) (default "json")

Global Flags:
      --skip-tls-verify   skip TLS certificate verification for container image registries while pulling bundles
      --use-http          use plain HTTP for container image registries while pulling bundles
```

Example command usage:
```
# Example with file argument passed in
opm alpha render-template semver infile.semver.template.yaml

# Example with no file argument passed in
opm alpha render-template semver -o yaml < infile.semver.template.yaml > outfile.yaml

# Example with "-" as the file argument passed in
cat infile.semver.template.yaml | opm alpha render-template semver -o mermaid -
```
Note that if the command is called without a file argument and nothing passed in on standard input,
the command will hang indefinitely. Either a file argument or file information passed 
in on standard input is required by the command.

With the template attribute `GenerateMajorChannels: true` resulting major channels from the command are (filtering out `olm.bundle` content):
```yaml
---
defaultChannel: stable-v1
name: testoperator
schema: olm.package
---
entries:
  - name: testoperator.v0.1.0
  - name: testoperator.v0.1.1
  - name: testoperator.v0.1.2
  - name: testoperator.v0.1.3
    skips:
      - testoperator.v0.1.0
      - testoperator.v0.1.1
      - testoperator.v0.1.2
  - name: testoperator.v0.2.0
  - name: testoperator.v0.2.1
  - name: testoperator.v0.2.2
    replaces: testoperator.v0.1.3
    skips:
      - testoperator.v0.1.0
      - testoperator.v0.1.1
      - testoperator.v0.1.2
      - testoperator.v0.2.0
      - testoperator.v0.2.1
  - name: testoperator.v0.3.0
    replaces: testoperator.v0.2.2
    skips:
      - testoperator.v0.1.0
      - testoperator.v0.1.1
      - testoperator.v0.1.2
      - testoperator.v0.1.3
      - testoperator.v0.2.0
      - testoperator.v0.2.1
name: candidate-v0
package: testoperator
schema: olm.channel
---
entries:
  - name: testoperator.v1.0.0
  - name: testoperator.v1.0.1
    skips:
      - testoperator.v1.0.0
  - name: testoperator.v1.1.0
    replaces: testoperator.v1.0.1
    skips:
      - testoperator.v1.0.0
name: candidate-v1
package: testoperator
schema: olm.channel
---
entries:
  - name: testoperator.v0.2.1
  - name: testoperator.v0.2.2
    skips:
      - testoperator.v0.2.1
  - name: testoperator.v0.3.0
    replaces: testoperator.v0.2.2
    skips:
      - testoperator.v0.2.1
name: fast-v0
package: testoperator
schema: olm.channel
---
entries:
  - name: testoperator.v1.0.1
  - name: testoperator.v1.1.0
    replaces: testoperator.v1.0.1
name: fast-v1
package: testoperator
schema: olm.channel
---
entries:
  - name: testoperator.v1.0.1
name: stable-v1
package: testoperator
schema: olm.channel
```

We generated a channel for each template channel entity corresponding to each of the 0.\#.\#, 1.\#.\# major version ranges with skips to the head of the highest semver in a channel.  We also generated a replaces edge to traverse across minor version transitions within each major channel.  Finally, we generated an `olm.package` object, setting as default the most-stable channel head we created.  This process will prefer `Stable` channel over `Fast`, over `Candidate` and then a higher bundle version over a lower version.   
(Please note that the naming of the generated channels indicates the digits of significance for that channel.  For example, `fast-v1` is a decomposed channel of the `fast` type which contains only major versions of contributing bundles matching `v1`.)  

For contrast, with the template attribute `GenerateMinorChannels: true` and running the command again (again skipping rendered bundle image output) we get a bunch more channels:
```yaml
---
defaultChannel: stable-v1.0
name: testoperator
schema: olm.package
---
entries:
  - name: testoperator.v0.1.0
  - name: testoperator.v0.1.1
  - name: testoperator.v0.1.2
  - name: testoperator.v0.1.3
    skips:
      - testoperator.v0.1.0
      - testoperator.v0.1.1
      - testoperator.v0.1.2
name: candidate-v0.1
package: testoperator
schema: olm.channel
---
entries:
  - name: testoperator.v0.2.0
  - name: testoperator.v0.2.1
  - name: testoperator.v0.2.2
    replaces: testoperator.v0.1.3
    skips:
      - testoperator.v0.1.0
      - testoperator.v0.1.1
      - testoperator.v0.1.2
      - testoperator.v0.2.0
      - testoperator.v0.2.1
name: candidate-v0.2
package: testoperator
schema: olm.channel
---
entries:
  - name: testoperator.v0.3.0
    replaces: testoperator.v0.2.2
    skips:
      - testoperator.v0.1.0
      - testoperator.v0.1.1
      - testoperator.v0.1.2
      - testoperator.v0.1.3
      - testoperator.v0.2.0
      - testoperator.v0.2.1
name: candidate-v0.3
package: testoperator
schema: olm.channel
---
entries:
  - name: testoperator.v1.0.0
  - name: testoperator.v1.0.1
    skips:
      - testoperator.v1.0.0
name: candidate-v1.0
package: testoperator
schema: olm.channel
---
entries:
  - name: testoperator.v1.1.0
    replaces: testoperator.v1.0.1
    skips:
      - testoperator.v1.0.0
name: candidate-v1.1
package: testoperator
schema: olm.channel
---
entries:
  - name: testoperator.v0.2.1
  - name: testoperator.v0.2.2
    skips:
      - testoperator.v0.2.1
name: fast-v0.2
package: testoperator
schema: olm.channel
---
entries:
  - name: testoperator.v0.3.0
    replaces: testoperator.v0.2.2
    skips:
      - testoperator.v0.2.1
name: fast-v0.3
package: testoperator
schema: olm.channel
---
entries:
  - name: testoperator.v1.0.1
name: fast-v1.0
package: testoperator
schema: olm.channel
---
entries:
  - name: testoperator.v1.1.0
    replaces: testoperator.v1.0.1
name: fast-v1.1
package: testoperator
schema: olm.channel
---
entries:
  - name: testoperator.v1.0.1
name: stable-v1.0
package: testoperator
schema: olm.channel
```
Here, a channel is generated for each template channel which differs by minor version, each channel has a `replaces` edge from the highest version entry in the predecessor channel, and the highest version entry in each channel also has a skips list composed of all lower version entries within the same minor (Y).  Please note that at no time do we transgress across major-version boundaries with the channels, to be consistent with [the semver convention](https://semver.org/) for major versions, where the purpose is to make incompatible API changes.


### DEMOS

#### Major Channel Generation
![`GenerateMajorChannels`](./major-version-demo.gif)

#### Minor Channel Generation
![`GenerateMinorChannels`](./minor-version-demo.gif)


