# Import the function to set the global logger provider from the OpenTelemetry logs module.
from opentelemetry._logs import set_logger_provider

# Import the OTLPLogExporter class from the OpenTelemetry gRPC log exporter module.
from opentelemetry.exporter.otlp.proto.grpc._log_exporter import OTLPLogExporter

# Import the LoggerProvider and LoggingHandler classes from the OpenTelemetry SDK logs module.
from opentelemetry.sdk._logs import LoggerProvider, LoggingHandler

# Import the BatchLogRecordProcessor class from the OpenTelemetry SDK logs export module.
from opentelemetry.sdk._logs.export import BatchLogRecordProcessor

# Import the Resource class from the OpenTelemetry SDK resources module.
from opentelemetry.sdk.resources import Resource

# Import the logging module.
import logging

from dotenv import load_dotenv
import os


class CustomLogFW:
    """
    CustomLogFW sets up logging using OpenTelemetry with a specified service name and instance ID.
    """
    
    def __init__(self, service_name, instance_id):
        """
        Initialize the CustomLogFW with a service name and instance ID.

        :param service_name: Name of the service for logging purposes.
        :param instance_id: Unique instance ID of the service.
        """
        # Create an instance of LoggerProvider with a Resource object that includes
        # service name and instance ID, identifying the source of the logs.
        self.logger_provider = LoggerProvider(
            resource=Resource.create(
                {
                    "service.name": service_name,
                    "service.instance.id": instance_id,
                }
            )
        )

    def setup_logging(self):
        """
        Set up the logging configuration.

        :return: LoggingHandler instance configured with the logger provider.
        """
        # Set the created LoggerProvider as the global logger provider.
        set_logger_provider(self.logger_provider)

        # Create an instance of OTLPLogExporter with insecure connection.        
        otlpEndpoint = os.getenv("OTLP_ENDPOINT")
        otlpHeaders = os.getenv("OTLP_HEADERS")
        exporter = OTLPLogExporter()

        # Add a BatchLogRecordProcessor to the logger provider with the exporter.
        self.logger_provider.add_log_record_processor(BatchLogRecordProcessor(exporter=exporter, max_export_batch_size=5))

        # Create a LoggingHandler with the specified logger provider and log level set to NOTSET.
        handler = LoggingHandler(level=logging.NOTSET, logger_provider=self.logger_provider)

        return handler
