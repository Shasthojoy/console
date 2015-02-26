angular.module('k8s')
.service('k8sPods', function(_, k8sDocker, k8sUtil, k8sEnum) {
  'use strict';

  this.clean = function(pod) {
    k8sUtil.nullifyEmpty(pod.metadata, ['annotations', 'labels']);
    k8sUtil.nullifyEmpty(pod.spec, ['volumes']);
    _.each(pod.spec.containers, function(c) {
      k8sDocker.clean(c);
    });
  };

  this.getEmpty = function(ns) {
    return {
      metadata: {
        annotations: [],
        labels: [],
        name: null,
        namespace: ns || k8sEnum.DefaultNS,
      },
      spec: {
        containers: [],
        dnsPolicy: null,
        restartPolicy: null,
        volumes: [],
      },
    };
  };

  this.getEmptyVolume = function() {
    return {
      name: null,
      source: {
        emptyDir: null,
        gitRepo: null,
        hostPath: null,
        persistentDisk: null,
      }
    };
  };

});