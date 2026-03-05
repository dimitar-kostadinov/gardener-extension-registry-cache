<p>Packages:</p>
<ul>
<li>
<a href="#spegel.extensions.gardener.cloud%2fv1alpha1">spegel.extensions.gardener.cloud/v1alpha1</a>
</li>
</ul>
<h2 id="spegel.extensions.gardener.cloud/v1alpha1">spegel.extensions.gardener.cloud/v1alpha1</h2>
<p>
<p>Package v1alpha1 is a version of the API.</p>
</p>
Resource Types:
<ul></ul>
<h3 id="spegel.extensions.gardener.cloud/v1alpha1.SpegelConfig">SpegelConfig
</h3>
<p>
<p>SpegelConfig contains information about the Spegel listening addresses of each Node.</p>
</p>
<table>
<thead>
<tr>
<th>Field</th>
<th>Description</th>
</tr>
</thead>
<tbody>
<tr>
<td>
<code>registryPort</code></br>
<em>
int32
</em>
</td>
<td>
<em>(Optional)</em>
<p>RegistryPort is the port that serves the OCI registry on each Node.
<code>registryPort</code> should be a valid port number (1-65535, inclusive).
Defaults to 15500.</p>
</td>
</tr>
<tr>
<td>
<code>routerPort</code></br>
<em>
int32
</em>
</td>
<td>
<em>(Optional)</em>
<p>RouterPort is the port for P2P router on each Node.
<code>routerPort</code> should be a valid port number (1-65535, inclusive).
Defaults to 15501.</p>
</td>
</tr>
<tr>
<td>
<code>metricsPort</code></br>
<em>
int32
</em>
</td>
<td>
<em>(Optional)</em>
<p>MetricsPort is the metrics port on each Node.
<code>metricsPort</code> should be a valid port number (1-65535, inclusive).
Defaults to 19090.</p>
</td>
</tr>
</tbody>
</table>
<h3 id="spegel.extensions.gardener.cloud/v1alpha1.SpegelStatus">SpegelStatus
</h3>
<p>
<p>SpegelStatus contains information about Spegel client TLS secrets.</p>
</p>
<table>
<thead>
<tr>
<th>Field</th>
<th>Description</th>
</tr>
</thead>
<tbody>
<tr>
<td>
<code>caSecretName</code></br>
<em>
string
</em>
</td>
<td>
<p>CASecretName is the name of the CA bundle secret.</p>
</td>
</tr>
<tr>
<td>
<code>clientTLSSecretName</code></br>
<em>
string
</em>
</td>
<td>
<p>ClientTLSSecretName is the name ot the Spegel client TLS secret.</p>
</td>
</tr>
</tbody>
</table>
<hr/>
<p><em>
Generated with <a href="https://github.com/ahmetb/gen-crd-api-reference-docs">gen-crd-api-reference-docs</a>
</em></p>
