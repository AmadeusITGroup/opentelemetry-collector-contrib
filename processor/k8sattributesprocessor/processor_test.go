// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package k8sattributesprocessor

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/collector/client"
	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/component/componentstatus"
	"go.opentelemetry.io/collector/component/componenttest"
	"go.opentelemetry.io/collector/consumer"
	"go.opentelemetry.io/collector/consumer/consumertest"
	"go.opentelemetry.io/collector/consumer/xconsumer"
	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/plog"
	"go.opentelemetry.io/collector/pdata/pmetric"
	"go.opentelemetry.io/collector/pdata/pprofile"
	"go.opentelemetry.io/collector/pdata/ptrace"
	"go.opentelemetry.io/collector/processor"
	"go.opentelemetry.io/collector/processor/processortest"
	"go.opentelemetry.io/collector/processor/xprocessor"
	conventions "go.opentelemetry.io/otel/semconv/v1.8.0"

	"github.com/open-telemetry/opentelemetry-collector-contrib/internal/k8sconfig"
	"github.com/open-telemetry/opentelemetry-collector-contrib/processor/k8sattributesprocessor/internal/kube"
	"github.com/open-telemetry/opentelemetry-collector-contrib/processor/k8sattributesprocessor/internal/metadata"
)

func newPodIdentifier(from, name, value string) kube.PodIdentifier {
	if from == kube.ConnectionSource {
		return kube.PodIdentifier{
			kube.PodIdentifierAttributeFromConnection(value),
		}
	}

	return kube.PodIdentifier{
		kube.PodIdentifierAttributeFromResourceAttribute(name, value),
	}
}

func newTracesProcessor(cfg component.Config, next consumer.Traces, options ...option) (processor.Traces, error) {
	opts := options
	opts = append(opts, withKubeClientProvider(newFakeClient))
	set := processortest.NewNopSettings(metadata.Type)
	return createTracesProcessorWithOptions(
		context.Background(),
		set,
		cfg,
		next,
		opts...,
	)
}

func newMetricsProcessor(cfg component.Config, nextMetricsConsumer consumer.Metrics, options ...option) (processor.Metrics, error) {
	opts := options
	opts = append(opts, withKubeClientProvider(newFakeClient))
	set := processortest.NewNopSettings(metadata.Type)
	return createMetricsProcessorWithOptions(
		context.Background(),
		set,
		cfg,
		nextMetricsConsumer,
		opts...,
	)
}

func newLogsProcessor(cfg component.Config, nextLogsConsumer consumer.Logs, options ...option) (processor.Logs, error) {
	opts := options
	opts = append(opts, withKubeClientProvider(newFakeClient))
	set := processortest.NewNopSettings(metadata.Type)
	return createLogsProcessorWithOptions(
		context.Background(),
		set,
		cfg,
		nextLogsConsumer,
		opts...,
	)
}

func newProfilesProcessor(cfg component.Config, nextProfilesConsumer xconsumer.Profiles, options ...option) (xprocessor.Profiles, error) {
	opts := options
	opts = append(opts, withKubeClientProvider(newFakeClient))
	set := processortest.NewNopSettings(metadata.Type)
	return createProfilesProcessorWithOptions(
		context.Background(),
		set,
		cfg,
		nextProfilesConsumer,
		opts...,
	)
}

// withKubeClientProvider sets the specific implementation for getting K8s Client instances
func withKubeClientProvider(kcp kube.ClientProvider) option {
	return func(p *kubernetesprocessor) error {
		return p.initKubeClient(p.telemetrySettings, kcp)
	}
}

// withExtractKubernetesProcessorInto allows to pull the internal model easily even when processorhelper factory is used
func withExtractKubernetesProcessorInto(kp **kubernetesprocessor) option {
	return func(p *kubernetesprocessor) error {
		*kp = p
		return nil
	}
}

type multiTest struct {
	t *testing.T

	tp processor.Traces
	mp processor.Metrics
	lp processor.Logs
	pp xprocessor.Profiles

	nextTrace    *consumertest.TracesSink
	nextMetrics  *consumertest.MetricsSink
	nextLogs     *consumertest.LogsSink
	nextProfiles *consumertest.ProfilesSink

	kpMetrics  *kubernetesprocessor
	kpTrace    *kubernetesprocessor
	kpLogs     *kubernetesprocessor
	kpProfiles *kubernetesprocessor
}

func newMultiTest(
	t *testing.T,
	cfg component.Config,
	errFunc func(err error),
	options ...option,
) *multiTest {
	m := &multiTest{
		t:            t,
		nextTrace:    new(consumertest.TracesSink),
		nextMetrics:  new(consumertest.MetricsSink),
		nextLogs:     new(consumertest.LogsSink),
		nextProfiles: new(consumertest.ProfilesSink),
	}

	tp, err := newTracesProcessor(cfg, m.nextTrace, append(options, withExtractKubernetesProcessorInto(&m.kpTrace))...)
	require.NoError(t, err)
	err = tp.Start(context.Background(), &nopHost{
		reportFunc: func(event *componentstatus.Event) {
			errFunc(event.Err())
		},
	})
	if errFunc == nil {
		assert.NotNil(t, tp)
		require.NoError(t, err)
	}

	mp, err := newMetricsProcessor(cfg, m.nextMetrics, append(options, withExtractKubernetesProcessorInto(&m.kpMetrics))...)
	require.NoError(t, err)
	err = mp.Start(context.Background(), &nopHost{
		reportFunc: func(event *componentstatus.Event) {
			errFunc(event.Err())
		},
	})
	if errFunc == nil {
		assert.NotNil(t, mp)
		require.NoError(t, err)
	}

	lp, err := newLogsProcessor(cfg, m.nextLogs, append(options, withExtractKubernetesProcessorInto(&m.kpLogs))...)
	require.NoError(t, err)
	err = lp.Start(context.Background(), &nopHost{
		reportFunc: func(event *componentstatus.Event) {
			errFunc(event.Err())
		},
	})
	if errFunc == nil {
		assert.NotNil(t, lp)
		require.NoError(t, err)
	}

	pp, err := newProfilesProcessor(cfg, m.nextProfiles, append(options, withExtractKubernetesProcessorInto(&m.kpProfiles))...)
	require.NoError(t, err)
	err = pp.Start(context.Background(), &nopHost{
		reportFunc: func(event *componentstatus.Event) {
			errFunc(event.Err())
		},
	})
	if errFunc == nil {
		assert.NotNil(t, pp)
		require.NoError(t, err)
	}

	m.tp = tp
	m.mp = mp
	m.lp = lp
	m.pp = pp
	return m
}

func (m *multiTest) testConsume(
	ctx context.Context,
	traces ptrace.Traces,
	metrics pmetric.Metrics,
	logs plog.Logs,
	profiles pprofile.Profiles,
	errFunc func(err error),
) {
	errs := []error{
		m.tp.ConsumeTraces(ctx, traces),
		m.mp.ConsumeMetrics(ctx, metrics),
		m.lp.ConsumeLogs(ctx, logs),
		m.pp.ConsumeProfiles(ctx, profiles),
	}

	for _, err := range errs {
		if errFunc != nil {
			errFunc(err)
		}
	}
}

func (m *multiTest) kubernetesProcessorOperation(kpOp func(kp *kubernetesprocessor)) {
	kpOp(m.kpTrace)
	kpOp(m.kpMetrics)
	kpOp(m.kpLogs)
	kpOp(m.kpProfiles)
}

func (m *multiTest) assertBatchesLen(batchesLen int) {
	require.Len(m.t, m.nextTrace.AllTraces(), batchesLen)
	require.Len(m.t, m.nextMetrics.AllMetrics(), batchesLen)
	require.Len(m.t, m.nextLogs.AllLogs(), batchesLen)
	require.Len(m.t, m.nextProfiles.AllProfiles(), batchesLen)
}

func (m *multiTest) assertResourceObjectLen(batchNo int) {
	assert.Equal(m.t, 1, m.nextTrace.AllTraces()[batchNo].ResourceSpans().Len())
	assert.Equal(m.t, 1, m.nextMetrics.AllMetrics()[batchNo].ResourceMetrics().Len())
	assert.Equal(m.t, 1, m.nextLogs.AllLogs()[batchNo].ResourceLogs().Len())
	assert.Equal(m.t, 1, m.nextProfiles.AllProfiles()[batchNo].ResourceProfiles().Len())
}

func (m *multiTest) assertResourceAttributesLen(batchNo, attrsLen int) {
	assert.Equal(m.t, attrsLen, m.nextTrace.AllTraces()[batchNo].ResourceSpans().At(0).Resource().Attributes().Len())
	assert.Equal(m.t, attrsLen, m.nextMetrics.AllMetrics()[batchNo].ResourceMetrics().At(0).Resource().Attributes().Len())
	assert.Equal(m.t, attrsLen, m.nextLogs.AllLogs()[batchNo].ResourceLogs().At(0).Resource().Attributes().Len())
	assert.Equal(m.t, attrsLen, m.nextProfiles.AllProfiles()[batchNo].ResourceProfiles().At(0).Resource().Attributes().Len())
}

func (m *multiTest) assertResource(batchNum int, resourceFunc func(res pcommon.Resource)) {
	rss := m.nextTrace.AllTraces()[batchNum].ResourceSpans()
	r := rss.At(0).Resource()

	if resourceFunc != nil {
		resourceFunc(r)
	}
}

func TestNewProcessor(t *testing.T) {
	cfg := NewFactory().CreateDefaultConfig()

	newMultiTest(t, cfg, nil)
}

func TestProcessorBadClientProvider(t *testing.T) {
	clientProvider := func(_ component.TelemetrySettings, _ k8sconfig.APIConfig, _ kube.ExtractionRules, _ kube.Filters, _ []kube.Association, _ kube.Excludes, _ kube.APIClientsetProvider, _ kube.InformersFactoryList, _ bool, _ time.Duration) (kube.Client, error) {
		return nil, errors.New("bad client error")
	}

	newMultiTest(t, NewFactory().CreateDefaultConfig(), func(err error) {
		require.EqualError(t, err, "bad client error")
	}, withKubeClientProvider(clientProvider))
}

type generateResourceFunc func(res pcommon.Resource)

func generateTraces(resourceFunc ...generateResourceFunc) ptrace.Traces {
	t := ptrace.NewTraces()
	rs := t.ResourceSpans().AppendEmpty()
	for _, resFun := range resourceFunc {
		res := rs.Resource()
		resFun(res)
	}
	span := rs.ScopeSpans().AppendEmpty().Spans().AppendEmpty()
	span.SetName("foobar")
	return t
}

func generateMetrics(resourceFunc ...generateResourceFunc) pmetric.Metrics {
	m := pmetric.NewMetrics()
	ms := m.ResourceMetrics().AppendEmpty()
	for _, resFun := range resourceFunc {
		res := ms.Resource()
		resFun(res)
	}
	metric := ms.ScopeMetrics().AppendEmpty().Metrics().AppendEmpty()
	metric.SetName("foobar")
	return m
}

func generateLogs(resourceFunc ...generateResourceFunc) plog.Logs {
	l := plog.NewLogs()
	ls := l.ResourceLogs().AppendEmpty()
	for _, resFun := range resourceFunc {
		res := ls.Resource()
		resFun(res)
	}
	ls.ScopeLogs().AppendEmpty().LogRecords().AppendEmpty()
	return l
}

func generateProfiles(resourceFunc ...generateResourceFunc) pprofile.Profiles {
	p := pprofile.NewProfiles()
	ps := p.ResourceProfiles().AppendEmpty()
	for _, resFun := range resourceFunc {
		res := ps.Resource()
		resFun(res)
	}
	ps.ScopeProfiles().AppendEmpty().Profiles().AppendEmpty()
	return p
}

func withPassthroughIP(passthroughIP string) generateResourceFunc {
	return func(res pcommon.Resource) {
		res.Attributes().PutStr(kube.K8sIPLabelName, passthroughIP)
	}
}

func withHostname(hostname string) generateResourceFunc {
	return func(res pcommon.Resource) {
		res.Attributes().PutStr(string(conventions.HostNameKey), hostname)
	}
}

func withPodUID(uid string) generateResourceFunc {
	return func(res pcommon.Resource) {
		res.Attributes().PutStr("k8s.pod.uid", uid)
	}
}

func withContainerName(containerName string) generateResourceFunc {
	return func(res pcommon.Resource) {
		res.Attributes().PutStr(string(conventions.K8SContainerNameKey), containerName)
	}
}

func withContainerID(id string) generateResourceFunc {
	return func(res pcommon.Resource) {
		res.Attributes().PutStr(string(conventions.ContainerIDKey), id)
	}
}

func withContainerRunID(containerRunID string) generateResourceFunc {
	return func(res pcommon.Resource) {
		res.Attributes().PutStr(string(conventions.K8SContainerRestartCountKey), containerRunID)
	}
}

type strAddr string

func (strAddr) String() string {
	return "1.1.1.1:3200"
}

func (strAddr) Network() string {
	return "tcp"
}

func TestIPDetectionFromContext(t *testing.T) {
	addresses := []net.Addr{
		&net.IPAddr{
			IP: net.IPv4(1, 1, 1, 1),
		},
		&net.TCPAddr{
			IP:   net.IPv4(1, 1, 1, 1),
			Port: 3200,
		},
		&net.UDPAddr{
			IP:   net.IPv4(1, 1, 1, 1),
			Port: 3200,
		},
		strAddr("1.1.1.1:3200"),
	}
	for _, addr := range addresses {
		m := newMultiTest(t, NewFactory().CreateDefaultConfig(), nil)
		ctx := client.NewContext(context.Background(), client.Info{
			Addr: addr,
		})
		m.testConsume(
			ctx,
			generateTraces(),
			generateMetrics(),
			generateLogs(),
			generateProfiles(),
			func(err error) {
				assert.NoError(t, err)
			})

		m.assertBatchesLen(1)
		m.assertResourceObjectLen(0)
		m.assertResource(0, func(r pcommon.Resource) {
			require.Positive(t, r.Attributes().Len())
			assertResourceHasStringAttribute(t, r, "k8s.pod.ip", "1.1.1.1")
		})
	}
}

func TestNilBatch(t *testing.T) {
	m := newMultiTest(t, NewFactory().CreateDefaultConfig(), nil)
	m.testConsume(
		context.Background(),
		ptrace.NewTraces(),
		pmetric.NewMetrics(),
		generateLogs(),
		generateProfiles(),
		func(err error) {
			assert.NoError(t, err)
		})

	m.assertBatchesLen(1)
}

func TestProcessorNoAttrs(t *testing.T) {
	m := newMultiTest(
		t,
		NewFactory().CreateDefaultConfig(),
		nil,
		withExtractMetadata(string(conventions.K8SPodNameKey)),
	)

	ctx := client.NewContext(context.Background(), client.Info{
		Addr: &net.IPAddr{
			IP: net.IPv4(1, 1, 1, 1),
		},
	})

	// pod doesn't have attrs to add
	m.kubernetesProcessorOperation(func(kp *kubernetesprocessor) {
		pi := kube.PodIdentifier{
			kube.PodIdentifierAttributeFromConnection("1.1.1.1"),
		}
		kp.kc.(*fakeClient).Pods[pi] = &kube.Pod{Name: "PodA"}
	})

	m.testConsume(
		ctx,
		generateTraces(),
		generateMetrics(),
		generateLogs(),
		generateProfiles(),
		func(err error) {
			assert.NoError(t, err)
		})

	m.assertBatchesLen(1)
	m.assertResourceObjectLen(0)
	m.assertResourceAttributesLen(0, 1)

	// attrs should be added now
	m.kubernetesProcessorOperation(func(kp *kubernetesprocessor) {
		pi := kube.PodIdentifier{
			kube.PodIdentifierAttributeFromConnection("1.1.1.1"),
		}

		kp.kc.(*fakeClient).Pods[pi] = &kube.Pod{
			Name: "PodA",
			Attributes: map[string]string{
				"k":  "v",
				"1":  "2",
				"aa": "b",
			},
		}
	})

	m.testConsume(
		ctx,
		generateTraces(),
		generateMetrics(),
		generateLogs(),
		generateProfiles(),
		func(err error) {
			assert.NoError(t, err)
		})

	m.assertBatchesLen(2)
	m.assertResourceObjectLen(1)
	m.assertResourceAttributesLen(1, 4)

	// passthrough doesn't add attrs
	m.kubernetesProcessorOperation(func(kp *kubernetesprocessor) {
		kp.passthroughMode = true
	})
	m.testConsume(
		ctx,
		generateTraces(),
		generateMetrics(),
		generateLogs(),
		generateProfiles(),
		func(err error) {
			assert.NoError(t, err)
		})

	m.assertBatchesLen(3)
	m.assertResourceObjectLen(2)
	m.assertResourceAttributesLen(2, 1)
}

func TestNoIP(t *testing.T) {
	m := newMultiTest(
		t,
		NewFactory().CreateDefaultConfig(),
		nil,
	)

	m.testConsume(context.Background(), generateTraces(), generateMetrics(), generateLogs(), generateProfiles(), nil)

	m.assertBatchesLen(1)
	m.assertResourceObjectLen(0)
	m.assertResource(0, func(res pcommon.Resource) {
		assert.Equal(t, 0, res.Attributes().Len())
	})
}

func TestIPSourceWithoutPodAssociation(t *testing.T) {
	m := newMultiTest(
		t,
		NewFactory().CreateDefaultConfig(),
		nil,
	)

	type testCase struct {
		name, resourceIP, resourceK8SIP, out string
		contextIP                            net.IP
	}

	testCases := []testCase{
		{
			name:          "k8sIP",
			resourceIP:    "1.1.1.1",
			resourceK8SIP: "2.2.2.2",
			contextIP:     net.IPv4(3, 3, 3, 3),
			out:           "2.2.2.2",
		},
		{
			name:       "clientIP",
			resourceIP: "1.1.1.1",
			contextIP:  net.IPv4(3, 3, 3, 3),
			out:        "1.1.1.1",
		},
		{
			name:      "contextIP",
			contextIP: net.IPv4(3, 3, 3, 3),
			out:       "3.3.3.3",
		},
	}

	for i, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			if tc.contextIP != nil {
				ctx = client.NewContext(context.Background(), client.Info{
					Addr: &net.IPAddr{
						IP: tc.contextIP,
					},
				})
			}

			traces := generateTraces()
			metrics := generateMetrics()
			logs := generateLogs()
			profiles := generateProfiles()

			resources := []pcommon.Resource{
				traces.ResourceSpans().At(0).Resource(),
				metrics.ResourceMetrics().At(0).Resource(),
			}

			for _, res := range resources {
				if tc.resourceK8SIP != "" {
					res.Attributes().PutStr(kube.K8sIPLabelName, tc.resourceK8SIP)
				}
				if tc.resourceIP != "" {
					res.Attributes().PutStr(clientIPLabelName, tc.resourceIP)
				}
			}

			m.testConsume(ctx, traces, metrics, logs, profiles, nil)
			m.assertBatchesLen(i + 1)
			m.assertResource(i, func(res pcommon.Resource) {
				require.Positive(t, res.Attributes().Len())
				assertResourceHasStringAttribute(t, res, "k8s.pod.ip", tc.out)
			})
		})
	}
}

func TestIPSourceWithPodAssociation(t *testing.T) {
	m := newMultiTest(
		t,
		NewFactory().CreateDefaultConfig(),
		nil,
	)

	type testCase struct {
		name, labelName, labelValue, outLabel, outValue string
	}

	testCases := []testCase{
		{
			name:       "k8sIP",
			labelName:  "k8s.pod.ip",
			labelValue: "1.1.1.1",
			outLabel:   "k8s.pod.ip",
			outValue:   "1.1.1.1",
		},
		{
			name:       "client IP",
			labelName:  "ip",
			labelValue: "2.2.2.2",
			outLabel:   "ip",
			outValue:   "2.2.2.2",
		},
	}
	m.kubernetesProcessorOperation(func(kp *kubernetesprocessor) {
		kp.podAssociations = []kube.Association{
			{
				Name: "k8s.pod.ip",
				Sources: []kube.AssociationSource{
					{
						From: "resource_attribute",
						Name: "k8s.pod.ip",
					},
				},
			},
			{
				Name: "k8s.pod.ip",
				Sources: []kube.AssociationSource{
					{
						From: "resource_attribute",
						Name: "ip",
					},
				},
			},
			{
				Name: "k8s.pod.ip",
				Sources: []kube.AssociationSource{
					{
						From: "resource_attribute",
						Name: "host.name",
					},
				},
			},
		}
	})

	for i, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			traces := generateTraces()
			metrics := generateMetrics()
			logs := generateLogs()
			profiles := generateProfiles()

			resources := []pcommon.Resource{
				traces.ResourceSpans().At(0).Resource(),
				metrics.ResourceMetrics().At(0).Resource(),
				logs.ResourceLogs().At(0).Resource(),
				profiles.ResourceProfiles().At(0).Resource(),
			}

			for _, res := range resources {
				res.Attributes().PutStr(tc.labelName, tc.labelValue)
			}

			m.testConsume(ctx, traces, metrics, logs, profiles, nil)
			m.assertBatchesLen(i + 1)
			m.assertResource(i, func(res pcommon.Resource) {
				require.Positive(t, res.Attributes().Len())
				assertResourceHasStringAttribute(t, res, tc.outLabel, tc.outValue)
			})
		})
	}
}

func TestPodUID(t *testing.T) {
	m := newMultiTest(
		t,
		NewFactory().CreateDefaultConfig(),
		nil,
	)
	m.kubernetesProcessorOperation(func(kp *kubernetesprocessor) {
		kp.podAssociations = []kube.Association{
			{
				Sources: []kube.AssociationSource{
					{
						From: "resource_attribute",
						Name: "k8s.pod.uid",
					},
				},
			},
		}
		kp.kc.(*fakeClient).Pods[newPodIdentifier("resource_attribute", "k8s.pod.uid", "ef10d10b-2da5-4030-812e-5f45c1531227")] = &kube.Pod{
			Name: "PodA",
			Attributes: map[string]string{
				"k":  "v",
				"1":  "2",
				"aa": "b",
			},
		}
	})

	m.testConsume(context.Background(),
		generateTraces(withPodUID("ef10d10b-2da5-4030-812e-5f45c1531227")),
		generateMetrics(withPodUID("ef10d10b-2da5-4030-812e-5f45c1531227")),
		generateLogs(withPodUID("ef10d10b-2da5-4030-812e-5f45c1531227")),
		generateProfiles(withPodUID("ef10d10b-2da5-4030-812e-5f45c1531227")),
		nil)

	m.assertBatchesLen(1)
	m.assertResourceObjectLen(0)
	m.assertResource(0, func(r pcommon.Resource) {
		require.Positive(t, r.Attributes().Len())
		assertResourceHasStringAttribute(t, r, "k8s.pod.uid", "ef10d10b-2da5-4030-812e-5f45c1531227")
	})
}

func TestAddPodLabels(t *testing.T) {
	m := newMultiTest(
		t,
		NewFactory().CreateDefaultConfig(),
		nil,
	)

	tests := map[string]map[string]string{
		"1.1.1.1": {
			"pod":         "test-2323",
			"ns":          "default",
			"another tag": "value",
		},
		"2.2.2.2": {
			"pod": "test-12",
		},
	}
	m.kubernetesProcessorOperation(func(kp *kubernetesprocessor) {
		kp.podAssociations = []kube.Association{
			{
				Sources: []kube.AssociationSource{
					{
						From: "connection",
					},
				},
			},
		}
	})

	for ip, attrs := range tests {
		m.kubernetesProcessorOperation(func(kp *kubernetesprocessor) {
			pi := kube.PodIdentifier{
				kube.PodIdentifierAttributeFromConnection(ip),
			}
			kp.kc.(*fakeClient).Pods[pi] = &kube.Pod{Attributes: attrs}
		})
	}

	var i int
	for ip, attrs := range tests {
		ctx := client.NewContext(context.Background(), client.Info{
			Addr: &net.IPAddr{
				IP: net.ParseIP(ip),
			},
		})
		m.testConsume(
			ctx,
			generateTraces(),
			generateMetrics(),
			generateLogs(),
			generateProfiles(),
			func(err error) {
				assert.NoError(t, err)
			})

		m.assertBatchesLen(i + 1)
		m.assertResourceObjectLen(i)
		m.assertResource(i, func(res pcommon.Resource) {
			require.Positive(t, res.Attributes().Len())
			assertResourceHasStringAttribute(t, res, "k8s.pod.ip", ip)
			for k, v := range attrs {
				assertResourceHasStringAttribute(t, res, k, v)
			}
		})

		i++
	}
}

func TestAddNamespaceLabels(t *testing.T) {
	m := newMultiTest(
		t,
		func() component.Config {
			cfg := createDefaultConfig().(*Config)
			cfg.Extract.Metadata = []string{string(conventions.ServiceNamespaceKey)}
			cfg.Extract.Labels = []FieldExtractConfig{
				{
					From: kube.MetadataFromNamespace,
					Key:  "namespace-label",
				},
			}
			return cfg
		}(),
		nil,
	)

	podIP := "1.1.1.1"
	namespaces := map[string]map[string]string{
		"namespace-1": {
			"nslabel": "1",
		},
		"namespace-2": {
			"nslabel": "2",
		},
	}
	m.kubernetesProcessorOperation(func(kp *kubernetesprocessor) {
		kp.podAssociations = []kube.Association{
			{
				Sources: []kube.AssociationSource{
					{
						From: "connection",
					},
				},
			},
		}
	})

	m.kubernetesProcessorOperation(func(kp *kubernetesprocessor) {
		pi := kube.PodIdentifier{
			kube.PodIdentifierAttributeFromConnection(podIP),
		}
		kp.kc.(*fakeClient).Pods[pi] = &kube.Pod{Name: "test-2323", Namespace: "namespace-1"}
		kp.kc.(*fakeClient).Namespaces = make(map[string]*kube.Namespace)
		for ns, labels := range namespaces {
			kp.kc.(*fakeClient).Namespaces[ns] = &kube.Namespace{Attributes: labels}
		}
	})

	ctx := client.NewContext(context.Background(), client.Info{
		Addr: &net.IPAddr{
			IP: net.ParseIP(podIP),
		},
	})
	m.testConsume(
		ctx,
		generateTraces(),
		generateMetrics(),
		generateLogs(),
		generateProfiles(),
		func(err error) {
			assert.NoError(t, err)
		})

	m.assertBatchesLen(1)
	m.assertResourceObjectLen(0)
	m.assertResource(0, func(res pcommon.Resource) {
		assert.Equal(t, 3, res.Attributes().Len())
		assertResourceHasStringAttribute(t, res, "k8s.pod.ip", podIP)
		assertResourceHasStringAttribute(t, res, "nslabel", "1")
		assertResourceHasStringAttribute(t, res, "service.namespace", "namespace-1")
	})
}

func TestAddNodeLabels(t *testing.T) {
	m := newMultiTest(
		t,
		func() component.Config {
			cfg := createDefaultConfig().(*Config)
			cfg.Extract.Metadata = []string{}
			cfg.Extract.Labels = []FieldExtractConfig{
				{
					From: kube.MetadataFromNode,
					Key:  "node-label",
				},
			}
			return cfg
		}(),
		nil,
	)

	podIP := "1.1.1.1"
	nodes := map[string]map[string]string{
		"node-1": {
			"nodelabel": "1",
		},
		"node-2": {
			"nodelabel": "2",
		},
	}
	m.kubernetesProcessorOperation(func(kp *kubernetesprocessor) {
		kp.podAssociations = []kube.Association{
			{
				Sources: []kube.AssociationSource{
					{
						From: "connection",
					},
				},
			},
		}
	})

	m.kubernetesProcessorOperation(func(kp *kubernetesprocessor) {
		pi := kube.PodIdentifier{
			kube.PodIdentifierAttributeFromConnection(podIP),
		}
		kp.kc.(*fakeClient).Pods[pi] = &kube.Pod{Name: "test-2323", NodeName: "node-1"}
		kp.kc.(*fakeClient).Nodes = make(map[string]*kube.Node)
		for ns, labels := range nodes {
			kp.kc.(*fakeClient).Nodes[ns] = &kube.Node{Attributes: labels}
		}
	})

	ctx := client.NewContext(context.Background(), client.Info{
		Addr: &net.IPAddr{
			IP: net.ParseIP(podIP),
		},
	})
	m.testConsume(
		ctx,
		generateTraces(),
		generateMetrics(),
		generateLogs(),
		generateProfiles(),
		func(err error) {
			assert.NoError(t, err)
		})

	m.assertBatchesLen(1)
	m.assertResourceObjectLen(0)
	m.assertResource(0, func(res pcommon.Resource) {
		assert.Equal(t, 2, res.Attributes().Len())
		assertResourceHasStringAttribute(t, res, "k8s.pod.ip", podIP)
		assertResourceHasStringAttribute(t, res, "nodelabel", "1")
	})
}

func TestAddNodeUID(t *testing.T) {
	nodeUID := "asdfasdf-asdfasdf-asdf"
	m := newMultiTest(
		t,
		func() component.Config {
			cfg := createDefaultConfig().(*Config)
			cfg.Extract.Metadata = []string{"k8s.node.uid"}
			cfg.Extract.Labels = []FieldExtractConfig{}
			return cfg
		}(),
		nil,
	)

	podIP := "1.1.1.1"
	nodes := map[string]map[string]string{
		"node-1": {
			"nodelabel": "1",
		},
	}
	m.kubernetesProcessorOperation(func(kp *kubernetesprocessor) {
		kp.podAssociations = []kube.Association{
			{
				Sources: []kube.AssociationSource{
					{
						From: "connection",
					},
				},
			},
		}
	})

	m.kubernetesProcessorOperation(func(kp *kubernetesprocessor) {
		pi := kube.PodIdentifier{
			kube.PodIdentifierAttributeFromConnection(podIP),
		}
		kp.kc.(*fakeClient).Pods[pi] = &kube.Pod{Name: "test-2323", NodeName: "node-1"}
		kp.kc.(*fakeClient).Nodes = make(map[string]*kube.Node)
		for ns, labels := range nodes {
			kp.kc.(*fakeClient).Nodes[ns] = &kube.Node{Attributes: labels, NodeUID: nodeUID}
		}
	})

	ctx := client.NewContext(context.Background(), client.Info{
		Addr: &net.IPAddr{
			IP: net.ParseIP(podIP),
		},
	})
	m.testConsume(
		ctx,
		generateTraces(),
		generateMetrics(),
		generateLogs(),
		generateProfiles(),
		func(err error) {
			assert.NoError(t, err)
		})

	m.assertBatchesLen(1)
	m.assertResourceObjectLen(0)
	m.assertResource(0, func(res pcommon.Resource) {
		assert.Equal(t, 3, res.Attributes().Len())
		assertResourceHasStringAttribute(t, res, "k8s.pod.ip", podIP)
		assertResourceHasStringAttribute(t, res, "k8s.node.uid", nodeUID)
		assertResourceHasStringAttribute(t, res, "nodelabel", "1")
	})
}

func TestProcessorAddContainerAttributes(t *testing.T) {
	tests := []struct {
		name         string
		op           func(kp *kubernetesprocessor)
		resourceGens []generateResourceFunc
		wantAttrs    map[string]any
	}{
		{
			name: "all-by-name",
			op: func(kp *kubernetesprocessor) {
				kp.podAssociations = []kube.Association{
					{
						Name: "k8s.pod.uid",
						Sources: []kube.AssociationSource{
							{
								From: "resource_attribute",
								Name: "k8s.pod.uid",
							},
						},
					},
				}
				kp.kc.(*fakeClient).Pods[newPodIdentifier("resource_attribute", "k8s.pod.uid", "19f651bc-73e4-410f-b3e9-f0241679d3b8")] = &kube.Pod{
					Containers: kube.PodContainers{
						ByName: map[string]*kube.Container{
							"app": {
								Name:              "app",
								ImageName:         "test/app",
								ImageTag:          "1.0.1",
								ServiceInstanceID: "instance-1",
								ServiceVersion:    "1.0.1",
							},
						},
					},
				}
			},
			resourceGens: []generateResourceFunc{
				withPodUID("19f651bc-73e4-410f-b3e9-f0241679d3b8"),
				withContainerName("app"),
			},
			wantAttrs: map[string]any{
				string(conventions.K8SPodUIDKey):          "19f651bc-73e4-410f-b3e9-f0241679d3b8",
				string(conventions.K8SContainerNameKey):   "app",
				string(conventions.ContainerImageNameKey): "test/app",
				string(conventions.ContainerImageTagKey):  "1.0.1",
				string(conventions.ServiceInstanceIDKey):  "instance-1",
				string(conventions.ServiceVersionKey):     "1.0.1",
			},
		},
		{
			name: "all-by-id",
			op: func(kp *kubernetesprocessor) {
				kp.podAssociations = []kube.Association{
					{
						Name: "k8s.pod.uid",
						Sources: []kube.AssociationSource{
							{
								From: "resource_attribute",
								Name: "k8s.pod.uid",
							},
						},
					},
				}
				kp.kc.(*fakeClient).Pods[newPodIdentifier("resource_attribute", "k8s.pod.uid", "19f651bc-73e4-410f-b3e9-f0241679d3b8")] = &kube.Pod{
					Containers: kube.PodContainers{
						ByID: map[string]*kube.Container{
							"767dc30d4fece77038e8ec2585a33471944d0b754659af7aa7e101181418f0dd": {
								Name:      "app",
								ImageName: "test/app",
								ImageTag:  "1.0.1",
							},
						},
					},
				}
			},
			resourceGens: []generateResourceFunc{
				withPodUID("19f651bc-73e4-410f-b3e9-f0241679d3b8"),
				withContainerID("767dc30d4fece77038e8ec2585a33471944d0b754659af7aa7e101181418f0dd"),
			},
			wantAttrs: map[string]any{
				string(conventions.K8SPodUIDKey):          "19f651bc-73e4-410f-b3e9-f0241679d3b8",
				string(conventions.ContainerIDKey):        "767dc30d4fece77038e8ec2585a33471944d0b754659af7aa7e101181418f0dd",
				string(conventions.K8SContainerNameKey):   "app",
				string(conventions.ContainerImageNameKey): "test/app",
				string(conventions.ContainerImageTagKey):  "1.0.1",
			},
		},
		{
			name: "automatic-explicit-values-win",
			op: func(kp *kubernetesprocessor) {
				kp.podAssociations = []kube.Association{
					{
						Name: "k8s.pod.uid",
						Sources: []kube.AssociationSource{
							{
								From: "resource_attribute",
								Name: "k8s.pod.uid",
							},
						},
					},
				}
				kp.kc.(*fakeClient).Pods[newPodIdentifier("resource_attribute", "k8s.pod.uid", "19f651bc-73e4-410f-b3e9-f0241679d3b8")] = &kube.Pod{
					Attributes: map[string]string{
						string(conventions.ServiceInstanceIDKey): "explicit-instance",
						string(conventions.ServiceVersionKey):    "explicit-version",
						string(conventions.ServiceNameKey):       "explicit-name",
						string(conventions.ServiceNamespaceKey):  "explicit-ns",
					},
					Containers: kube.PodContainers{
						ByID: map[string]*kube.Container{
							"767dc30d4fece77038e8ec2585a33471944d0b754659af7aa7e101181418f0dd": {
								Name:              "app",
								ImageName:         "test/app",
								ImageTag:          "1.0.1",
								ServiceInstanceID: "instance-1",
								ServiceVersion:    "version-1",
							},
						},
					},
				}
			},
			resourceGens: []generateResourceFunc{
				withPodUID("19f651bc-73e4-410f-b3e9-f0241679d3b8"),
				withContainerID("767dc30d4fece77038e8ec2585a33471944d0b754659af7aa7e101181418f0dd"),
			},
			wantAttrs: map[string]any{
				string(conventions.K8SPodUIDKey):          "19f651bc-73e4-410f-b3e9-f0241679d3b8",
				string(conventions.ContainerIDKey):        "767dc30d4fece77038e8ec2585a33471944d0b754659af7aa7e101181418f0dd",
				string(conventions.K8SContainerNameKey):   "app",
				string(conventions.ContainerImageNameKey): "test/app",
				string(conventions.ContainerImageTagKey):  "1.0.1",
				string(conventions.ServiceInstanceIDKey):  "explicit-instance",
				string(conventions.ServiceVersionKey):     "explicit-version",
				string(conventions.ServiceNameKey):        "explicit-name",
				string(conventions.ServiceNamespaceKey):   "explicit-ns",
			},
		},
		{
			name: "image-only",
			op: func(kp *kubernetesprocessor) {
				kp.podAssociations = []kube.Association{
					{
						Name: "k8s.pod.uid",
						Sources: []kube.AssociationSource{
							{
								From: "resource_attribute",
								Name: "k8s.pod.uid",
							},
						},
					},
				}
				kp.kc.(*fakeClient).Pods[newPodIdentifier("resource_attribute", "k8s.pod.uid", "19f651bc-73e4-410f-b3e9-f0241679d3b8")] = &kube.Pod{
					Containers: kube.PodContainers{
						ByName: map[string]*kube.Container{
							"app": {
								ImageName: "test/app",
								ImageTag:  "1.0.1",
							},
						},
					},
				}
			},
			resourceGens: []generateResourceFunc{
				withPodUID("19f651bc-73e4-410f-b3e9-f0241679d3b8"),
				withContainerName("app"),
			},
			wantAttrs: map[string]any{
				string(conventions.K8SPodUIDKey):          "19f651bc-73e4-410f-b3e9-f0241679d3b8",
				string(conventions.K8SContainerNameKey):   "app",
				string(conventions.ContainerImageNameKey): "test/app",
				string(conventions.ContainerImageTagKey):  "1.0.1",
			},
		},
		{
			name: "container-id-with-runid",
			op: func(kp *kubernetesprocessor) {
				kp.kc.(*fakeClient).Pods[newPodIdentifier("connection", "k8s.pod.ip", "1.1.1.1")] = &kube.Pod{
					Containers: kube.PodContainers{
						ByName: map[string]*kube.Container{
							"app": {
								Statuses: map[int]kube.ContainerStatus{
									0: {ContainerID: "fcd58c97330c1dc6615bd520031f6a703a7317cd92adc96013c4dd57daad0b5f"},
									1: {ContainerID: "6a7f1a598b5dafec9c193f8f8d63f6e5839b8b0acd2fe780f94285e26c05580e"},
									2: {ContainerID: "5ba4e0e5a5eb1f37bc6e7fc76495914400a3ee309d8828d16407e4b3d5410848"},
								},
							},
						},
					},
				}
			},
			resourceGens: []generateResourceFunc{
				withPassthroughIP("1.1.1.1"),
				withContainerName("app"),
				withContainerRunID("1"),
			},
			wantAttrs: map[string]any{
				kube.K8sIPLabelName:                             "1.1.1.1",
				string(conventions.K8SContainerNameKey):         "app",
				string(conventions.K8SContainerRestartCountKey): "1",
				string(conventions.ContainerIDKey):              "6a7f1a598b5dafec9c193f8f8d63f6e5839b8b0acd2fe780f94285e26c05580e",
			},
		},
		{
			name: "container-id-latest",
			op: func(kp *kubernetesprocessor) {
				kp.kc.(*fakeClient).Pods[newPodIdentifier("connection", "k8s.pod.ip", "1.1.1.1")] = &kube.Pod{
					Containers: kube.PodContainers{
						ByName: map[string]*kube.Container{
							"app": {
								Statuses: map[int]kube.ContainerStatus{
									0: {ContainerID: "fcd58c97330c1dc6615bd520031f6a703a7317cd92adc96013c4dd57daad0b5f"},
									1: {ContainerID: "6a7f1a598b5dafec9c193f8f8d63f6e5839b8b0acd2fe780f94285e26c05580e"},
									2: {ContainerID: "5ba4e0e5a5eb1f37bc6e7fc76495914400a3ee309d8828d16407e4b3d5410848"},
								},
							},
						},
					},
				}
			},
			resourceGens: []generateResourceFunc{
				withPassthroughIP("1.1.1.1"),
				withContainerName("app"),
			},
			wantAttrs: map[string]any{
				kube.K8sIPLabelName:                     "1.1.1.1",
				string(conventions.K8SContainerNameKey): "app",
				string(conventions.ContainerIDKey):      "5ba4e0e5a5eb1f37bc6e7fc76495914400a3ee309d8828d16407e4b3d5410848",
			},
		},
		{
			name: "container-repo-digests",
			op: func(kp *kubernetesprocessor) {
				kp.kc.(*fakeClient).Pods[newPodIdentifier("connection", "k8s.pod.ip", "1.1.1.1")] = &kube.Pod{
					Containers: kube.PodContainers{
						ByName: map[string]*kube.Container{
							"app": {
								Statuses: map[int]kube.ContainerStatus{
									2: {ImageRepoDigest: "docker.io/otel/collector:1.2.3@sha256:deadbeef02"},
								},
							},
						},
					},
				}
			},
			resourceGens: []generateResourceFunc{
				withPassthroughIP("1.1.1.1"),
				withContainerName("app"),
			},
			wantAttrs: map[string]any{
				kube.K8sIPLabelName:                     "1.1.1.1",
				string(conventions.K8SContainerNameKey): "app",
				containerImageRepoDigests:               []string{"docker.io/otel/collector:1.2.3@sha256:deadbeef02"},
			},
		},
		{
			name: "container-name-mismatch",
			op: func(kp *kubernetesprocessor) {
				kp.kc.(*fakeClient).Pods[newPodIdentifier("connection", "k8s.pod.ip", "1.1.1.1")] = &kube.Pod{
					Containers: kube.PodContainers{
						ByName: map[string]*kube.Container{
							"app": {
								ImageName: "test/app",
								ImageTag:  "1.0.1",
								Statuses: map[int]kube.ContainerStatus{
									0: {ContainerID: "fcd58c97330c1dc6615bd520031f6a703a7317cd92adc96013c4dd57daad0b5f"},
								},
							},
						},
					},
				}
			},
			resourceGens: []generateResourceFunc{
				withPassthroughIP("1.1.1.1"),
				withContainerName("new-app"),
				withContainerRunID("0"),
			},
			wantAttrs: map[string]any{
				kube.K8sIPLabelName:                             "1.1.1.1",
				string(conventions.K8SContainerNameKey):         "new-app",
				string(conventions.K8SContainerRestartCountKey): "0",
			},
		},
		{
			name: "container-run-id-mismatch",
			op: func(kp *kubernetesprocessor) {
				kp.kc.(*fakeClient).Pods[newPodIdentifier("connection", "k8s.pod.ip", "1.1.1.1")] = &kube.Pod{
					Containers: kube.PodContainers{
						ByName: map[string]*kube.Container{
							"app": {
								ImageName: "test/app",
								Statuses: map[int]kube.ContainerStatus{
									0: {ContainerID: "fcd58c97330c1dc6615bd520031f6a703a7317cd92adc96013c4dd57daad0b5f"},
								},
							},
						},
					},
				}
			},
			resourceGens: []generateResourceFunc{
				withPassthroughIP("1.1.1.1"),
				withContainerName("app"),
				withContainerRunID("1"),
			},
			wantAttrs: map[string]any{
				kube.K8sIPLabelName:                             "1.1.1.1",
				string(conventions.K8SContainerNameKey):         "app",
				string(conventions.K8SContainerRestartCountKey): "1",
				string(conventions.ContainerImageNameKey):       "test/app",
			},
		},
		{
			name: "fall back to only container",
			op: func(kp *kubernetesprocessor) {
				kp.podAssociations = []kube.Association{
					{
						Name: "k8s.pod.uid",
						Sources: []kube.AssociationSource{
							{
								From: "resource_attribute",
								Name: "k8s.pod.uid",
							},
						},
					},
				}
				kp.kc.(*fakeClient).Pods[newPodIdentifier("resource_attribute", "k8s.pod.uid", "19f651bc-73e4-410f-b3e9-f0241679d3b8")] = &kube.Pod{
					Containers: kube.PodContainers{
						ByName: map[string]*kube.Container{
							"app": {
								Name:      "app",
								ImageName: "test/app",
								ImageTag:  "1.0.1",
							},
						},
					},
				}
			},
			resourceGens: []generateResourceFunc{
				withPodUID("19f651bc-73e4-410f-b3e9-f0241679d3b8"),
			},
			wantAttrs: map[string]any{
				string(conventions.K8SPodUIDKey):          "19f651bc-73e4-410f-b3e9-f0241679d3b8",
				string(conventions.K8SContainerNameKey):   "app",
				string(conventions.ContainerImageNameKey): "test/app",
				string(conventions.ContainerImageTagKey):  "1.0.1",
			},
		},
		{
			name: "multiple containers in the pod - do not fall back to any container",
			op: func(kp *kubernetesprocessor) {
				kp.podAssociations = []kube.Association{
					{
						Name: "k8s.pod.uid",
						Sources: []kube.AssociationSource{
							{
								From: "resource_attribute",
								Name: "k8s.pod.uid",
							},
						},
					},
				}
				kp.kc.(*fakeClient).Pods[newPodIdentifier("resource_attribute", "k8s.pod.uid", "19f651bc-73e4-410f-b3e9-f0241679d3b8")] = &kube.Pod{
					Containers: kube.PodContainers{
						ByName: map[string]*kube.Container{
							"app": {
								Name:      "app",
								ImageName: "test/app",
								ImageTag:  "1.0.1",
							},
							"app2": {
								Name:      "app2",
								ImageName: "test/app",
								ImageTag:  "1.0.1",
							},
						},
					},
				}
			},
			resourceGens: []generateResourceFunc{
				withPodUID("19f651bc-73e4-410f-b3e9-f0241679d3b8"),
			},
			wantAttrs: map[string]any{
				string(conventions.K8SPodUIDKey): "19f651bc-73e4-410f-b3e9-f0241679d3b8",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := newMultiTest(
				t,
				NewFactory().CreateDefaultConfig(),
				nil,
				withExtractMetadata(
					string(conventions.ServiceNamespaceKey),
					string(conventions.ServiceNameKey),
					string(conventions.ServiceVersionKey),
					string(conventions.ServiceInstanceIDKey),
				),
			)
			m.kubernetesProcessorOperation(tt.op)
			m.testConsume(context.Background(),
				generateTraces(tt.resourceGens...),
				generateMetrics(tt.resourceGens...),
				generateLogs(tt.resourceGens...),
				generateProfiles(tt.resourceGens...),
				nil,
			)

			m.assertBatchesLen(1)
			m.assertResource(0, func(r pcommon.Resource) {
				require.Len(t, r.Attributes().AsRaw(), len(tt.wantAttrs))
				for k, v := range tt.wantAttrs {
					switch val := v.(type) {
					case string:
						assertResourceHasStringAttribute(t, r, k, val)
					case []string:
						assertResourceHasStringSlice(t, r, k, val)
					}
				}
			})
		})
	}
}

func TestProcessorPicksUpPassthroughPodIp(t *testing.T) {
	m := newMultiTest(
		t,
		NewFactory().CreateDefaultConfig(),
		nil,
	)

	m.kubernetesProcessorOperation(func(kp *kubernetesprocessor) {
		kp.podAssociations = []kube.Association{
			{
				Name: "k8s.pod.ip",
				Sources: []kube.AssociationSource{
					{
						From: "resource_attribute",
						Name: "k8s.pod.ip",
					},
				},
			},
		}
		kp.kc.(*fakeClient).Pods[newPodIdentifier("resource_attribute", "k8s.pod.ip", "2.2.2.2")] = &kube.Pod{
			Name: "PodA",
			Attributes: map[string]string{
				"k": "v",
				"1": "2",
			},
		}
	})

	m.testConsume(
		context.Background(),
		generateTraces(withPassthroughIP("2.2.2.2")),
		generateMetrics(withPassthroughIP("2.2.2.2")),
		generateLogs(withPassthroughIP("2.2.2.2")),
		generateProfiles(withPassthroughIP("2.2.2.2")),
		func(err error) {
			assert.NoError(t, err)
		})

	m.assertBatchesLen(1)
	m.assertResourceObjectLen(0)
	m.assertResourceAttributesLen(0, 3)

	m.assertResource(0, func(res pcommon.Resource) {
		assertResourceHasStringAttribute(t, res, kube.K8sIPLabelName, "2.2.2.2")
		assertResourceHasStringAttribute(t, res, "k", "v")
		assertResourceHasStringAttribute(t, res, "1", "2")
	})
}

func TestMetricsProcessorHostname(t *testing.T) {
	next := new(consumertest.MetricsSink)
	var kp *kubernetesprocessor
	p, err := newMetricsProcessor(
		NewFactory().CreateDefaultConfig(),
		next,
		withExtractMetadata(string(conventions.K8SPodNameKey)),
		withExtractKubernetesProcessorInto(&kp),
	)
	require.NoError(t, err)
	err = p.Start(context.Background(), componenttest.NewNopHost())
	require.NoError(t, err)
	kc := kp.kc.(*fakeClient)

	// invalid ip should not be used to lookup k8s pod
	kc.Pods[newPodIdentifier("connection", "k8s.pod.ip", "invalid-ip")] = &kube.Pod{
		Name: "PodA",
		Attributes: map[string]string{
			"k":  "v",
			"1":  "2",
			"aa": "b",
		},
	}
	kc.Pods[newPodIdentifier("connection", "k8s.pod.ip", "3.3.3.3")] = &kube.Pod{
		Name: "PodA",
		Attributes: map[string]string{
			"kk": "vv",
		},
	}

	type testCase struct {
		name, hostname string
		expectedAttrs  map[string]string
	}

	testCases := []testCase{
		{
			name:     "invalid IP in hostname",
			hostname: "invalid-ip",
			expectedAttrs: map[string]string{
				string(conventions.HostNameKey): "invalid-ip",
			},
		},
		{
			name:     "valid IP in hostname",
			hostname: "3.3.3.3",
			expectedAttrs: map[string]string{
				string(conventions.HostNameKey): "3.3.3.3",
				kube.K8sIPLabelName:             "3.3.3.3",
				"kk":                            "vv",
			},
		},
	}

	for i, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			metrics := generateMetrics(withHostname(tc.hostname))
			assert.NoError(t, p.ConsumeMetrics(context.Background(), metrics))
			require.Len(t, next.AllMetrics(), i+1)

			md := next.AllMetrics()[i]
			require.Equal(t, 1, md.ResourceMetrics().Len())
			res := md.ResourceMetrics().At(0).Resource()
			assert.Equal(t, len(tc.expectedAttrs), res.Attributes().Len())
			for k, v := range tc.expectedAttrs {
				assertResourceHasStringAttribute(t, res, k, v)
			}
		})
	}
}

func TestMetricsProcessorHostnameWithPodAssociation(t *testing.T) {
	next := new(consumertest.MetricsSink)
	var kp *kubernetesprocessor
	p, err := newMetricsProcessor(
		NewFactory().CreateDefaultConfig(),
		next,
		withExtractMetadata(string(conventions.K8SPodNameKey)),
		withExtractKubernetesProcessorInto(&kp),
	)
	require.NoError(t, err)
	err = p.Start(context.Background(), componenttest.NewNopHost())
	require.NoError(t, err)
	kc := kp.kc.(*fakeClient)
	kp.podAssociations = []kube.Association{
		{
			Sources: []kube.AssociationSource{
				{
					From: "resource_attribute",
					Name: string(conventions.HostNameKey),
				},
			},
		},
	}

	kc.Pods[newPodIdentifier("resource_attribute", string(conventions.HostNameKey), "invalid-ip")] = &kube.Pod{
		Name: "PodA",
		Attributes: map[string]string{
			"k":  "v",
			"1":  "2",
			"aa": "b",
		},
	}
	kc.Pods[newPodIdentifier("resource_attribute", string(conventions.HostNameKey), "3.3.3.3")] = &kube.Pod{
		Name: "PodA",
		Attributes: map[string]string{
			"kk": "vv",
		},
	}

	type testCase struct {
		name, hostname string
		expectedAttrs  map[string]string
	}

	testCases := []testCase{
		{
			name:     "invalid IP in hostname",
			hostname: "invalid-ip",
			expectedAttrs: map[string]string{
				string(conventions.HostNameKey): "invalid-ip",
				"k":                             "v",
				"1":                             "2",
				"aa":                            "b",
			},
		},
		{
			name:     "valid IP in hostname",
			hostname: "3.3.3.3",
			expectedAttrs: map[string]string{
				string(conventions.HostNameKey): "3.3.3.3",
				"kk":                            "vv",
			},
		},
	}

	for i, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			metrics := generateMetrics(withHostname(tc.hostname))
			assert.NoError(t, p.ConsumeMetrics(context.Background(), metrics))
			require.Len(t, next.AllMetrics(), i+1)

			md := next.AllMetrics()[i]
			require.Equal(t, 1, md.ResourceMetrics().Len())
			res := md.ResourceMetrics().At(0).Resource()
			assert.Equal(t, len(tc.expectedAttrs), res.Attributes().Len())
			for k, v := range tc.expectedAttrs {
				assertResourceHasStringAttribute(t, res, k, v)
			}
		})
	}
}

func TestPassthroughStart(t *testing.T) {
	next := new(consumertest.TracesSink)
	opts := []option{withPassthrough()}

	p, err := newTracesProcessor(
		NewFactory().CreateDefaultConfig(),
		next,
		opts...,
	)
	require.NoError(t, err)

	// Just make sure this doesn't fail when Passthrough is enabled
	assert.NoError(t, p.Start(context.Background(), componenttest.NewNopHost()))
	assert.NoError(t, p.Shutdown(context.Background()))
}

func TestRealClient(t *testing.T) {
	newMultiTest(
		t,
		NewFactory().CreateDefaultConfig(),
		func(err error) {
			require.EqualError(t, err, "unable to load k8s config, KUBERNETES_SERVICE_HOST and KUBERNETES_SERVICE_PORT must be defined")
		},
		withKubeClientProvider(kubeClientProvider),
		withAPIConfig(k8sconfig.APIConfig{AuthType: "none"}),
	)
}

func TestCapabilities(t *testing.T) {
	p, err := newTracesProcessor(
		NewFactory().CreateDefaultConfig(),
		consumertest.NewNop(),
		nil,
	)
	assert.NoError(t, err)
	caps := p.Capabilities()
	assert.True(t, caps.MutatesData)
}

func TestStartStop(t *testing.T) {
	var kp *kubernetesprocessor
	p, err := newTracesProcessor(
		NewFactory().CreateDefaultConfig(),
		consumertest.NewNop(),
		withExtractKubernetesProcessorInto(&kp),
	)
	require.NoError(t, err)

	assert.NoError(t, p.Start(context.Background(), componenttest.NewNopHost()))
	assert.NoError(t, p.Start(context.Background(), componenttest.NewNopHost()))

	assert.NotNil(t, kp)
	kc := kp.kc.(*fakeClient)
	controller := kc.Informer.GetController().(*kube.FakeController)

	assert.False(t, controller.HasStopped())
	assert.NoError(t, p.Shutdown(context.Background()))
	time.Sleep(time.Millisecond * 500)
	assert.True(t, controller.HasStopped())
}

func assertResourceHasStringAttribute(t *testing.T, r pcommon.Resource, k, v string) {
	got, ok := r.Attributes().Get(k)
	require.Truef(t, ok, "resource does not contain attribute %s", k)
	assert.Equal(t, pcommon.ValueTypeStr, got.Type(), "attribute %s is not of type string", k)
	assert.Equal(t, v, got.Str(), "attribute %s is not equal to %s", k, v)
}

func assertResourceHasStringSlice(t *testing.T, r pcommon.Resource, k string, v []string) {
	got, ok := r.Attributes().Get(k)
	require.Truef(t, ok, "resource does not contain attribute %s", k)
	assert.Equal(t, pcommon.ValueTypeSlice, got.Type(), "attribute %s is not of type slice", k)
	slice := got.Slice()
	for i := 0; i < slice.Len(); i++ {
		assert.Equal(t, pcommon.ValueTypeStr, slice.At(i).Type())
		assert.Equal(t, v[i], slice.At(i).AsString(), "attribute %s[%d] is not equal to %s", k, i, v[i])
	}
}

func Test_intFromAttribute(t *testing.T) {
	tests := []struct {
		name    string
		attrVal pcommon.Value
		wantInt int
		wantErr bool
	}{
		{
			name:    "wrong-type",
			attrVal: pcommon.NewValueBool(true),
			wantInt: 0,
			wantErr: true,
		},
		{
			name:    "wrong-string-number",
			attrVal: pcommon.NewValueStr("NaN"),
			wantInt: 0,
			wantErr: true,
		},
		{
			name:    "valid-string-number",
			attrVal: pcommon.NewValueStr("3"),
			wantInt: 3,
			wantErr: false,
		},
		{
			name:    "valid-int-number",
			attrVal: pcommon.NewValueInt(1),
			wantInt: 1,
			wantErr: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := intFromAttribute(tt.attrVal)
			assert.Equal(t, tt.wantInt, got)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

var _ componentstatus.Reporter = (*nopHost)(nil)

type nopHost struct {
	reportFunc func(event *componentstatus.Event)
}

func (*nopHost) GetExtensions() map[component.ID]component.Component {
	return nil
}

func (nh *nopHost) Report(event *componentstatus.Event) {
	nh.reportFunc(event)
}

func Test_setResourceAttribute(t *testing.T) {
	tests := []struct {
		name       string
		attributes func() pcommon.Map
		key        string
		val        string
		wantAttrs  func() pcommon.Map
	}{
		{
			name:       "attribute not present - add value",
			attributes: pcommon.NewMap,
			key:        "foo",
			val:        "bar",
			wantAttrs: func() pcommon.Map {
				m := pcommon.NewMap()
				m.PutStr("foo", "bar")
				return m
			},
		},
		{
			name: "attribute present with non-empty value - do not overwrite value",
			attributes: func() pcommon.Map {
				m := pcommon.NewMap()
				m.PutStr("foo", "bar")
				return m
			},
			key: "foo",
			val: "baz",
			wantAttrs: func() pcommon.Map {
				m := pcommon.NewMap()
				m.PutStr("foo", "bar")
				return m
			},
		},
		{
			name: "attribute present with empty value - set value",
			attributes: func() pcommon.Map {
				m := pcommon.NewMap()
				m.PutStr("foo", "")
				return m
			},
			key: "foo",
			val: "bar",
			wantAttrs: func() pcommon.Map {
				m := pcommon.NewMap()
				m.PutStr("foo", "bar")
				return m
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			attrs := tt.attributes()
			setResourceAttribute(attrs, tt.key, tt.val)
			require.Equal(t, tt.wantAttrs(), attrs)
		})
	}
}
