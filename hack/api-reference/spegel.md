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
<code>registryAddress</code></br>
<em>
uint16
</em>
</td>
<td>
<em>(Optional)</em>
<p>RegistryPort is the port that serves the OCI registry on each Node.
Defaults to 5000.</p>
</td>
</tr>
<tr>
<td>
<code>routerAddress</code></br>
<em>
uint16
</em>
</td>
<td>
<em>(Optional)</em>
<p>RouterPort is the port for P2P router on each Node.
Defaults to 5001.</p>
</td>
</tr>
<tr>
<td>
<code>metricsAddress</code></br>
<em>
uint16
</em>
</td>
<td>
<em>(Optional)</em>
<p>MetricsPort is the metrics port on each Node.
Defaults to 9090.</p>
</td>
</tr>
</tbody>
</table>
<hr/>
<p><em>
Generated with <a href="https://github.com/ahmetb/gen-crd-api-reference-docs">gen-crd-api-reference-docs</a>
</em></p>
